package utilization

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
	"github.com/cockroachdb/pebble/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	linkKeyPrefix          = "link/"
	defaultStaleAfter      = 45 * time.Second
	defaultSweepAfter      = 10 * time.Minute
	defaultUncorrelatedTTL = 2 * time.Minute
)

type Options struct {
	StaleAfter      time.Duration
	SweepAfter      time.Duration
	UncorrelatedTTL time.Duration
}

func DefaultOptions() Options {
	return Options{StaleAfter: defaultStaleAfter, SweepAfter: defaultSweepAfter, UncorrelatedTTL: defaultUncorrelatedTTL}
}

func (o Options) withDefaults() Options {
	if o.StaleAfter <= 0 {
		o.StaleAfter = defaultStaleAfter
	}
	if o.SweepAfter <= 0 {
		o.SweepAfter = defaultSweepAfter
	}
	if o.UncorrelatedTTL <= 0 {
		o.UncorrelatedTTL = defaultUncorrelatedTTL
	}
	return o
}

type uncorrelatedRecord struct {
	iface *bgplsv1.UncorrelatedInterface
}

type utilSub struct {
	id      int
	linkIDs map[string]bool
	ch      chan *bgplsv1.WatchLinkUtilizationResponse
}

// Overlay stores the latest utilization sample per directed link, independently
// of the revisioned topology event journal.
type Overlay struct {
	db            *pebble.DB
	opts          Options
	mu            sync.RWMutex
	links         map[string]*bgplsv1.LinkUtilization
	uncorrelated  map[string]*uncorrelatedRecord
	index         *addrIndex
	nextSub       int
	subs          map[int]utilSub
	ambiguityHits uint64
}

func Open(path string, opts Options) (*Overlay, error) {
	opts = opts.withDefaults()
	db, err := pebble.Open(path, &pebble.Options{})
	if err != nil {
		return nil, fmt.Errorf("open utilization overlay: %w", err)
	}
	o := &Overlay{
		db:           db,
		opts:         opts,
		links:        map[string]*bgplsv1.LinkUtilization{},
		uncorrelated: map[string]*uncorrelatedRecord{},
		index:        newAddrIndex(nil),
		subs:         map[int]utilSub{},
	}
	if err := o.load(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return o, nil
}

func (o *Overlay) Close() error {
	if o == nil || o.db == nil {
		return nil
	}
	return o.db.Close()
}

func (o *Overlay) load() error {
	iter, err := o.db.NewIter(&pebble.IterOptions{LowerBound: []byte(linkKeyPrefix), UpperBound: prefixEnd(linkKeyPrefix)})
	if err != nil {
		return err
	}
	defer iter.Close()
	for iter.First(); iter.Valid(); iter.Next() {
		var u bgplsv1.LinkUtilization
		if err := json.Unmarshal(iter.Value(), &u); err != nil {
			return fmt.Errorf("decode utilization %s: %w", string(iter.Key()), err)
		}
		o.links[u.LinkId] = &u
	}
	return iter.Error()
}

func prefixEnd(prefix string) []byte {
	b := []byte(prefix)
	out := append([]byte(nil), b...)
	out[len(out)-1]++
	return out
}

func (o *Overlay) Serve(ctx context.Context) {
	if o == nil {
		return
	}
	ticker := time.NewTicker(o.opts.StaleAfter / 2)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := o.sweep(time.Now().UTC()); err != nil {
				slog.Error("utilization overlay sweep failed", "error", err)
			}
		}
	}
}

func (o *Overlay) RebuildIndex(links []*bgplsv1.Link) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.index = newAddrIndex(links)
}

func (o *Overlay) IndexLink(link *bgplsv1.Link) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.index.add(link)
}

func (o *Overlay) RemoveLink(id string) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	o.index.remove(id)
}

type ReportStats struct {
	Accepted     uint32
	Correlated   uint32
	Uncorrelated uint32
	Rejected     uint32
	Ambiguous    uint32
}

func (o *Overlay) Report(now time.Time, role string, reports []*bgplsv1.InterfaceUtilization) ReportStats {
	if o == nil {
		return ReportStats{}
	}
	var stats ReportStats
	for _, report := range reports {
		if report == nil || report.Device == "" || report.InterfaceName == "" {
			stats.Rejected++
			continue
		}
		stats.Accepted++
		o.mu.Lock()
		hit := o.index.lookup(report.Ipv4Addresses, report.Ipv6Addresses)
		switch {
		case hit.unnumbered:
			o.rememberUncorrelatedLocked(report, ReasonUnnumbered, now)
			stats.Uncorrelated++
		case hit.ambiguous:
			o.ambiguityHits++
			o.rememberUncorrelatedLocked(report, ReasonAmbiguous, now)
			stats.Uncorrelated++
			stats.Ambiguous++
			slog.Error("utilization correlation ambiguous", "device", report.Device, "interface", report.InterfaceName)
		case !hit.ok:
			o.rememberUncorrelatedLocked(report, ReasonNoMatch, now)
			stats.Uncorrelated++
		default:
			delete(o.uncorrelated, uncorrelatedKey(report.Device, report.InterfaceName))
			forward := deriveLinkUtilization(hit.forward.linkID, report.InBps, report.OutBps, report.SpeedBps, report.ObservedAt, now, o.opts.StaleAfter)
			o.putLocked(forward)
			if reverse, ok := o.index.reverseOf(hit.forward); ok {
				rev := deriveLinkUtilization(reverse.linkID, report.OutBps, report.InBps, report.SpeedBps, report.ObservedAt, now, o.opts.StaleAfter)
				o.putLocked(rev)
			}
			stats.Correlated++
		}
		o.mu.Unlock()
	}
	o.updateGauges()
	if role == "" {
		role = "unknown"
	}
	reportsTotal.WithLabelValues(role, "accepted").Add(float64(stats.Accepted))
	reportsTotal.WithLabelValues(role, "rejected").Add(float64(stats.Rejected))
	if stats.Ambiguous > 0 {
		ambiguityTotal.Add(float64(stats.Ambiguous))
	}
	return stats
}

func deriveLinkUtilization(linkID string, inBps, outBps, speedBps uint64, observed *timestamppb.Timestamp, now time.Time, staleAfter time.Duration) *bgplsv1.LinkUtilization {
	if observed == nil {
		observed = timestamppb.New(now)
	}
	u := &bgplsv1.LinkUtilization{
		LinkId:     linkID,
		InBps:      inBps,
		OutBps:     outBps,
		SpeedBps:   speedBps,
		ObservedAt: observed,
		StaleAt:    timestamppb.New(observed.AsTime().Add(staleAfter)),
	}
	ratio, available, ok := UtilizationRatio(inBps, outBps, speedBps)
	u.Utilization = ratio
	u.UtilizationKnown = ok
	u.AvailableBps = available
	return u
}

func (o *Overlay) putLocked(u *bgplsv1.LinkUtilization) {
	cloned := proto.Clone(u).(*bgplsv1.LinkUtilization)
	o.links[u.LinkId] = cloned
	if data, err := json.Marshal(cloned); err == nil {
		_ = o.db.Set([]byte(linkKeyPrefix+u.LinkId), data, pebble.NoSync)
	}
	o.publishLocked(&bgplsv1.WatchLinkUtilizationResponse{Utilization: cloned, Operation: "UPSERT"})
}

func (o *Overlay) rememberUncorrelatedLocked(report *bgplsv1.InterfaceUtilization, reason string, now time.Time) {
	observed := report.ObservedAt
	if observed == nil {
		observed = timestamppb.New(now)
	}
	o.uncorrelated[uncorrelatedKey(report.Device, report.InterfaceName)] = &uncorrelatedRecord{
		iface: &bgplsv1.UncorrelatedInterface{
			Device:        report.Device,
			InterfaceName: report.InterfaceName,
			Ipv4Addresses: append([]string(nil), report.Ipv4Addresses...),
			Ipv6Addresses: append([]string(nil), report.Ipv6Addresses...),
			Reason:        reason,
			ObservedAt:    observed,
		},
	}
}

func uncorrelatedKey(device, iface string) string { return device + "\x00" + iface }

func (o *Overlay) Get(id string) *bgplsv1.LinkUtilization {
	if o == nil {
		return nil
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	u := o.links[id]
	if u == nil {
		return nil
	}
	return proto.Clone(u).(*bgplsv1.LinkUtilization)
}

func (o *Overlay) List(linkIDs []string) []*bgplsv1.LinkUtilization {
	if o == nil {
		return nil
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	want := map[string]bool{}
	for _, id := range linkIDs {
		want[id] = true
	}
	var out []*bgplsv1.LinkUtilization
	for id, u := range o.links {
		if len(want) > 0 && !want[id] {
			continue
		}
		out = append(out, proto.Clone(u).(*bgplsv1.LinkUtilization))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LinkId < out[j].LinkId })
	return out
}

func (o *Overlay) Attach(links []*bgplsv1.Link) {
	if o == nil {
		return
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	for _, l := range links {
		if l == nil || l.GetMeta() == nil {
			continue
		}
		if u := o.links[l.GetMeta().GetId()]; u != nil {
			l.Utilization = proto.Clone(u).(*bgplsv1.LinkUtilization)
		}
	}
}

func (o *Overlay) Uncorrelated() []*bgplsv1.UncorrelatedInterface {
	if o == nil {
		return nil
	}
	now := time.Now().UTC()
	o.mu.Lock()
	defer o.mu.Unlock()
	o.expireUncorrelatedLocked(now)
	out := make([]*bgplsv1.UncorrelatedInterface, 0, len(o.uncorrelated))
	for _, rec := range o.uncorrelated {
		out = append(out, proto.Clone(rec.iface).(*bgplsv1.UncorrelatedInterface))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Device == out[j].Device {
			return out[i].InterfaceName < out[j].InterfaceName
		}
		return out[i].Device < out[j].Device
	})
	return out
}

func (o *Overlay) Subscribe(ctx context.Context, linkIDs []string) (<-chan *bgplsv1.WatchLinkUtilizationResponse, error) {
	if o == nil {
		return nil, errors.New("utilization overlay is not configured")
	}
	filter := map[string]bool{}
	for _, id := range linkIDs {
		if id != "" {
			filter[id] = true
		}
	}
	ch := make(chan *bgplsv1.WatchLinkUtilizationResponse, 256)
	o.mu.Lock()
	for _, u := range o.links {
		if len(filter) > 0 && !filter[u.LinkId] {
			continue
		}
		select {
		case ch <- &bgplsv1.WatchLinkUtilizationResponse{Utilization: proto.Clone(u).(*bgplsv1.LinkUtilization), Operation: "UPSERT"}:
		default:
		}
	}
	id := o.nextSub
	o.nextSub++
	o.subs[id] = utilSub{id: id, linkIDs: filter, ch: ch}
	o.mu.Unlock()
	go func() {
		<-ctx.Done()
		o.mu.Lock()
		if sub, ok := o.subs[id]; ok {
			delete(o.subs, id)
			close(sub.ch)
		}
		o.mu.Unlock()
	}()
	return ch, nil
}

func (o *Overlay) publishLocked(event *bgplsv1.WatchLinkUtilizationResponse) {
	for id, sub := range o.subs {
		if len(sub.linkIDs) > 0 && (event.Utilization == nil || !sub.linkIDs[event.Utilization.LinkId]) {
			continue
		}
		select {
		case sub.ch <- proto.Clone(event).(*bgplsv1.WatchLinkUtilizationResponse):
		default:
			delete(o.subs, id)
			close(sub.ch)
		}
	}
}

func (o *Overlay) sweep(now time.Time) error {
	cutoff := now.Add(-o.opts.SweepAfter)
	o.mu.Lock()
	defer o.mu.Unlock()
	o.expireUncorrelatedLocked(now)
	batch := o.db.NewBatch()
	defer batch.Close()
	for id, u := range o.links {
		if u.ObservedAt == nil || !u.ObservedAt.AsTime().Before(cutoff) {
			continue
		}
		delete(o.links, id)
		if err := batch.Delete([]byte(linkKeyPrefix+id), nil); err != nil {
			return err
		}
		o.publishLocked(&bgplsv1.WatchLinkUtilizationResponse{Utilization: &bgplsv1.LinkUtilization{LinkId: id}, Operation: "DELETE"})
	}
	if batch.Count() == 0 {
		o.updateGaugesLocked()
		return nil
	}
	if err := batch.Commit(pebble.NoSync); err != nil {
		return err
	}
	o.updateGaugesLocked()
	return nil
}

func (o *Overlay) expireUncorrelatedLocked(now time.Time) {
	for key, rec := range o.uncorrelated {
		if rec.iface.ObservedAt != nil && rec.iface.ObservedAt.AsTime().Add(o.opts.UncorrelatedTTL).Before(now) {
			delete(o.uncorrelated, key)
		}
	}
}

func (o *Overlay) updateGauges() {
	o.mu.RLock()
	defer o.mu.RUnlock()
	o.updateGaugesLocked()
}

func (o *Overlay) updateGaugesLocked() {
	linksCorrelated.Set(float64(len(o.links)))
	byReason := map[string]int{ReasonNoMatch: 0, ReasonAmbiguous: 0, ReasonUnnumbered: 0}
	for _, rec := range o.uncorrelated {
		byReason[rec.iface.Reason]++
	}
	var total int
	for reason, n := range byReason {
		uncorrelated.WithLabelValues(reason).Set(float64(n))
		total += n
	}
	linksUncorrelated.Set(float64(total))
}

func (o *Overlay) AmbiguityHits() uint64 {
	if o == nil {
		return 0
	}
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.ambiguityHits
}
