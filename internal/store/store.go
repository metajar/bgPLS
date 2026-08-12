package store

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
	"github.com/cockroachdb/pebble/v2"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	metaRevisionKey = "meta/revision"
	eventPrefix     = "event/"
	nodePrefix      = "node/"
	linkPrefix      = "link/"
	prefixPrefix    = "prefix/"
	peerPrefix      = "peer/"
	domainPrefix    = "domain/"
)

var ErrNotFound = errors.New("not found")

type Snapshot struct {
	Revision uint64
	Observed time.Time
	Domains  []*bgplsv1.Domain
	Nodes    []*bgplsv1.Node
	Links    []*bgplsv1.Link
	Prefixes []*bgplsv1.Prefix
	Peers    []*bgplsv1.Peer
}

type Mutation struct {
	Kind     bgplsv1.EntityKind
	ID       string
	DomainID string
	PeerID   string
	Reason   string
	Delete   bool
	Value    proto.Message
}

type subscription struct {
	id int
	ch chan *bgplsv1.TopologyEvent
}

// Store is an embedded current-state and event store. All writes are serialized
// so a revision and its event are committed in the same Pebble batch.
type Store struct {
	db       *pebble.DB
	mu       sync.RWMutex
	revision uint64
	observed time.Time
	nodes    map[string]*bgplsv1.Node
	links    map[string]*bgplsv1.Link
	prefixes map[string]*bgplsv1.Prefix
	peers    map[string]*bgplsv1.Peer
	domains  map[string]*bgplsv1.Domain
	nextSub  int
	subs     map[int]subscription
}

func Open(path string) (*Store, error) {
	db, err := pebble.Open(path, &pebble.Options{})
	if err != nil {
		return nil, fmt.Errorf("open topology store: %w", err)
	}
	s := &Store{db: db, nodes: map[string]*bgplsv1.Node{}, links: map[string]*bgplsv1.Link{}, prefixes: map[string]*bgplsv1.Prefix{}, peers: map[string]*bgplsv1.Peer{}, domains: map[string]*bgplsv1.Domain{}, subs: map[int]subscription{}}
	if err := s.load(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error      { return s.db.Close() }
func (s *Store) Revision() uint64  { s.mu.RLock(); defer s.mu.RUnlock(); return s.revision }
func (s *Store) DiskUsage() uint64 { return s.db.Metrics().DiskSpaceUsage() }

func (s *Store) load() error {
	if value, closer, err := s.db.Get([]byte(metaRevisionKey)); err == nil {
		if len(value) == 8 {
			s.revision = binary.BigEndian.Uint64(value)
		}
		_ = closer.Close()
	} else if !errors.Is(err, pebble.ErrNotFound) {
		return fmt.Errorf("load revision: %w", err)
	}
	if s.revision > 0 {
		if value, closer, err := s.db.Get(eventKey(s.revision)); err == nil {
			var event bgplsv1.TopologyEvent
			if json.Unmarshal(value, &event) == nil && event.ObservedAt != nil {
				s.observed = event.ObservedAt.AsTime()
			}
			_ = closer.Close()
		}
	}
	for _, spec := range []struct {
		prefix string
		load   func([]byte) error
	}{
		{domainPrefix, func(b []byte) error {
			var v bgplsv1.Domain
			if err := json.Unmarshal(b, &v); err != nil {
				return err
			}
			s.domains[v.Id] = &v
			return nil
		}},
		{nodePrefix, func(b []byte) error {
			var v bgplsv1.Node
			if err := json.Unmarshal(b, &v); err != nil {
				return err
			}
			s.nodes[v.GetMeta().GetId()] = &v
			return nil
		}},
		{linkPrefix, func(b []byte) error {
			var v bgplsv1.Link
			if err := json.Unmarshal(b, &v); err != nil {
				return err
			}
			s.links[v.GetMeta().GetId()] = &v
			return nil
		}},
		{prefixPrefix, func(b []byte) error {
			var v bgplsv1.Prefix
			if err := json.Unmarshal(b, &v); err != nil {
				return err
			}
			s.prefixes[v.GetMeta().GetId()] = &v
			return nil
		}},
		{peerPrefix, func(b []byte) error {
			var v bgplsv1.Peer
			if err := json.Unmarshal(b, &v); err != nil {
				return err
			}
			s.peers[v.Id] = &v
			return nil
		}},
	} {
		iter, err := s.db.NewIter(&pebble.IterOptions{LowerBound: []byte(spec.prefix), UpperBound: prefixEnd(spec.prefix)})
		if err != nil {
			return err
		}
		for iter.First(); iter.Valid(); iter.Next() {
			if err := spec.load(append([]byte(nil), iter.Value()...)); err != nil {
				_ = iter.Close()
				return fmt.Errorf("decode %s: %w", string(iter.Key()), err)
			}
		}
		if err := iter.Error(); err != nil {
			_ = iter.Close()
			return err
		}
		if err := iter.Close(); err != nil {
			return err
		}
	}
	return nil
}

func prefixEnd(prefix string) []byte {
	b := []byte(prefix)
	out := append([]byte(nil), b...)
	out[len(out)-1]++
	return out
}
func eventKey(rev uint64) []byte { return []byte(fmt.Sprintf("%s%020d", eventPrefix, rev)) }

func marshal(v any) ([]byte, error) { return json.Marshal(v) }

func (s *Store) Apply(ctx context.Context, m Mutation) (*bgplsv1.TopologyEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.ID == "" {
		return nil, errors.New("mutation id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	keyPrefix, current, err := s.lookupLocked(m.Kind, m.ID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	before, _ := marshal(current)
	now := time.Now().UTC()
	rev := s.revision + 1
	if !m.Delete {
		if m.Value == nil {
			return nil, errors.New("mutation value is required")
		}
		setRevision(m.Value, rev, now)
	}
	after, err := marshal(m.Value)
	if err != nil {
		return nil, fmt.Errorf("marshal entity: %w", err)
	}
	op := "UPSERT"
	if m.Delete {
		op = "DELETE"
		after = nil
	}
	event := &bgplsv1.TopologyEvent{Revision: rev, ObservedAt: timestamppb.New(now), EntityKind: m.Kind, EntityId: m.ID, DomainId: m.DomainID, Operation: op, SourcePeerId: m.PeerID, BeforeJson: before, AfterJson: after, Reason: m.Reason}
	eventBytes, err := marshal(event)
	if err != nil {
		return nil, err
	}
	batch := s.db.NewBatch()
	defer batch.Close()
	entityKey := []byte(keyPrefix + m.ID)
	if m.Delete {
		err = batch.Delete(entityKey, nil)
	} else {
		err = batch.Set(entityKey, after, nil)
	}
	if err == nil {
		err = batch.Set(eventKey(rev), eventBytes, nil)
	}
	var revBytes [8]byte
	binary.BigEndian.PutUint64(revBytes[:], rev)
	if err == nil {
		err = batch.Set([]byte(metaRevisionKey), revBytes[:], nil)
	}
	if err == nil {
		err = batch.Commit(pebble.Sync)
	}
	if err != nil {
		return nil, fmt.Errorf("commit topology revision: %w", err)
	}
	s.applyMemoryLocked(m)
	s.revision, s.observed = rev, now
	for id, sub := range s.subs {
		select {
		case sub.ch <- proto.Clone(event).(*bgplsv1.TopologyEvent):
		default:
			delete(s.subs, id)
			close(sub.ch)
		}
	}
	return proto.Clone(event).(*bgplsv1.TopologyEvent), nil
}

func setRevision(v proto.Message, rev uint64, now time.Time) {
	switch x := v.(type) {
	case *bgplsv1.Node:
		ensureMeta(x.Meta, &x.Meta, rev, now)
	case *bgplsv1.Link:
		ensureMeta(x.Meta, &x.Meta, rev, now)
	case *bgplsv1.Prefix:
		ensureMeta(x.Meta, &x.Meta, rev, now)
	case *bgplsv1.Peer:
		x.ResourceVersion = rev
	case *bgplsv1.Domain:
		x.Revision = rev
	}
}

func ensureMeta(meta *bgplsv1.EntityMeta, dst **bgplsv1.EntityMeta, rev uint64, now time.Time) {
	if meta == nil {
		meta = &bgplsv1.EntityMeta{}
		*dst = meta
	}
	meta.Revision = rev
	meta.LastSeen = timestamppb.New(now)
	if meta.FirstSeen == nil {
		meta.FirstSeen = timestamppb.New(now)
	}
	if meta.Freshness == bgplsv1.Freshness_FRESHNESS_UNSPECIFIED {
		meta.Freshness = bgplsv1.Freshness_FRESHNESS_ACTIVE
	}
}

func (s *Store) lookupLocked(kind bgplsv1.EntityKind, id string) (string, proto.Message, error) {
	switch kind {
	case bgplsv1.EntityKind_ENTITY_KIND_NODE:
		if v, ok := s.nodes[id]; ok {
			return nodePrefix, v, nil
		}
		return nodePrefix, nil, ErrNotFound
	case bgplsv1.EntityKind_ENTITY_KIND_LINK:
		if v, ok := s.links[id]; ok {
			return linkPrefix, v, nil
		}
		return linkPrefix, nil, ErrNotFound
	case bgplsv1.EntityKind_ENTITY_KIND_PREFIX:
		if v, ok := s.prefixes[id]; ok {
			return prefixPrefix, v, nil
		}
		return prefixPrefix, nil, ErrNotFound
	case bgplsv1.EntityKind_ENTITY_KIND_PEER:
		if v, ok := s.peers[id]; ok {
			return peerPrefix, v, nil
		}
		return peerPrefix, nil, ErrNotFound
	case bgplsv1.EntityKind_ENTITY_KIND_UNSPECIFIED:
		if v, ok := s.domains[id]; ok {
			return domainPrefix, v, nil
		}
		return domainPrefix, nil, ErrNotFound
	default:
		return "", nil, fmt.Errorf("unsupported entity kind %s", kind)
	}
}

func (s *Store) applyMemoryLocked(m Mutation) {
	if m.Delete {
		switch m.Kind {
		case bgplsv1.EntityKind_ENTITY_KIND_NODE:
			delete(s.nodes, m.ID)
		case bgplsv1.EntityKind_ENTITY_KIND_LINK:
			delete(s.links, m.ID)
		case bgplsv1.EntityKind_ENTITY_KIND_PREFIX:
			delete(s.prefixes, m.ID)
		case bgplsv1.EntityKind_ENTITY_KIND_PEER:
			delete(s.peers, m.ID)
		default:
			delete(s.domains, m.ID)
		}
		return
	}
	switch v := m.Value.(type) {
	case *bgplsv1.Node:
		s.nodes[m.ID] = proto.Clone(v).(*bgplsv1.Node)
	case *bgplsv1.Link:
		s.links[m.ID] = proto.Clone(v).(*bgplsv1.Link)
	case *bgplsv1.Prefix:
		s.prefixes[m.ID] = proto.Clone(v).(*bgplsv1.Prefix)
	case *bgplsv1.Peer:
		s.peers[m.ID] = proto.Clone(v).(*bgplsv1.Peer)
	case *bgplsv1.Domain:
		s.domains[m.ID] = proto.Clone(v).(*bgplsv1.Domain)
	}
}

func cloneSlice[T proto.Message](src []T) []T {
	out := make([]T, len(src))
	for i, v := range src {
		out[i] = proto.Clone(v).(T)
	}
	return out
}

func (s *Store) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := Snapshot{Revision: s.revision, Observed: s.observed}
	for _, v := range s.domains {
		snap.Domains = append(snap.Domains, proto.Clone(v).(*bgplsv1.Domain))
	}
	for _, v := range s.nodes {
		snap.Nodes = append(snap.Nodes, proto.Clone(v).(*bgplsv1.Node))
	}
	for _, v := range s.links {
		snap.Links = append(snap.Links, proto.Clone(v).(*bgplsv1.Link))
	}
	for _, v := range s.prefixes {
		snap.Prefixes = append(snap.Prefixes, proto.Clone(v).(*bgplsv1.Prefix))
	}
	for _, v := range s.peers {
		snap.Peers = append(snap.Peers, proto.Clone(v).(*bgplsv1.Peer))
	}
	sort.Slice(snap.Domains, func(i, j int) bool { return snap.Domains[i].Id < snap.Domains[j].Id })
	sort.Slice(snap.Nodes, func(i, j int) bool { return snap.Nodes[i].GetMeta().GetId() < snap.Nodes[j].GetMeta().GetId() })
	sort.Slice(snap.Links, func(i, j int) bool { return snap.Links[i].GetMeta().GetId() < snap.Links[j].GetMeta().GetId() })
	sort.Slice(snap.Prefixes, func(i, j int) bool { return snap.Prefixes[i].GetMeta().GetId() < snap.Prefixes[j].GetMeta().GetId() })
	sort.Slice(snap.Peers, func(i, j int) bool { return snap.Peers[i].Id < snap.Peers[j].Id })
	return snap
}

// SnapshotAt reconstructs a retained revision by undoing later events from a
// consistent current snapshot. Periodic on-disk checkpoints can be added behind
// this API without changing callers.
func (s *Store) SnapshotAt(revision uint64) (Snapshot, error) {
	snap := s.Snapshot()
	if revision == 0 || revision == snap.Revision {
		return snap, nil
	}
	if revision > snap.Revision {
		return Snapshot{}, fmt.Errorf("revision %d is newer than current revision %d", revision, snap.Revision)
	}
	oldest := s.OldestRevision()
	if oldest != 0 && revision+1 < oldest {
		return Snapshot{}, fmt.Errorf("revision %d has expired; oldest available revision is %d", revision, oldest-1)
	}
	nodes := map[string]*bgplsv1.Node{}
	links := map[string]*bgplsv1.Link{}
	prefixes := map[string]*bgplsv1.Prefix{}
	peers := map[string]*bgplsv1.Peer{}
	domains := map[string]*bgplsv1.Domain{}
	for _, v := range snap.Nodes {
		nodes[v.GetMeta().GetId()] = v
	}
	for _, v := range snap.Links {
		links[v.GetMeta().GetId()] = v
	}
	for _, v := range snap.Prefixes {
		prefixes[v.GetMeta().GetId()] = v
	}
	for _, v := range snap.Peers {
		peers[v.Id] = v
	}
	for _, v := range snap.Domains {
		domains[v.Id] = v
	}
	var events []*bgplsv1.TopologyEvent
	cursor := revision
	for cursor < snap.Revision {
		chunk, err := s.Events(cursor, snap.Revision, 10000)
		if err != nil {
			return Snapshot{}, err
		}
		if len(chunk) == 0 {
			break
		}
		events = append(events, chunk...)
		cursor = chunk[len(chunk)-1].Revision
	}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		undoEntity(event, nodes, links, prefixes, peers, domains)
	}
	out := Snapshot{Revision: revision}
	if marker, err := s.Events(revision-1, revision, 1); err == nil && len(marker) == 1 {
		out.Observed = marker[0].ObservedAt.AsTime()
	}
	for _, v := range nodes {
		out.Nodes = append(out.Nodes, v)
	}
	for _, v := range links {
		out.Links = append(out.Links, v)
	}
	for _, v := range prefixes {
		out.Prefixes = append(out.Prefixes, v)
	}
	for _, v := range peers {
		out.Peers = append(out.Peers, v)
	}
	for _, v := range domains {
		out.Domains = append(out.Domains, v)
	}
	sort.Slice(out.Nodes, func(i, j int) bool { return out.Nodes[i].GetMeta().GetId() < out.Nodes[j].GetMeta().GetId() })
	sort.Slice(out.Links, func(i, j int) bool { return out.Links[i].GetMeta().GetId() < out.Links[j].GetMeta().GetId() })
	sort.Slice(out.Prefixes, func(i, j int) bool { return out.Prefixes[i].GetMeta().GetId() < out.Prefixes[j].GetMeta().GetId() })
	sort.Slice(out.Peers, func(i, j int) bool { return out.Peers[i].Id < out.Peers[j].Id })
	sort.Slice(out.Domains, func(i, j int) bool { return out.Domains[i].Id < out.Domains[j].Id })
	return out, nil
}

func undoEntity(event *bgplsv1.TopologyEvent, nodes map[string]*bgplsv1.Node, links map[string]*bgplsv1.Link, prefixes map[string]*bgplsv1.Prefix, peers map[string]*bgplsv1.Peer, domains map[string]*bgplsv1.Domain) {
	if len(event.BeforeJson) == 0 || string(event.BeforeJson) == "null" {
		switch event.EntityKind {
		case bgplsv1.EntityKind_ENTITY_KIND_NODE:
			delete(nodes, event.EntityId)
		case bgplsv1.EntityKind_ENTITY_KIND_LINK:
			delete(links, event.EntityId)
		case bgplsv1.EntityKind_ENTITY_KIND_PREFIX:
			delete(prefixes, event.EntityId)
		case bgplsv1.EntityKind_ENTITY_KIND_PEER:
			delete(peers, event.EntityId)
		default:
			delete(domains, event.EntityId)
		}
		return
	}
	switch event.EntityKind {
	case bgplsv1.EntityKind_ENTITY_KIND_NODE:
		var v bgplsv1.Node
		if json.Unmarshal(event.BeforeJson, &v) == nil {
			nodes[event.EntityId] = &v
		}
	case bgplsv1.EntityKind_ENTITY_KIND_LINK:
		var v bgplsv1.Link
		if json.Unmarshal(event.BeforeJson, &v) == nil {
			links[event.EntityId] = &v
		}
	case bgplsv1.EntityKind_ENTITY_KIND_PREFIX:
		var v bgplsv1.Prefix
		if json.Unmarshal(event.BeforeJson, &v) == nil {
			prefixes[event.EntityId] = &v
		}
	case bgplsv1.EntityKind_ENTITY_KIND_PEER:
		var v bgplsv1.Peer
		if json.Unmarshal(event.BeforeJson, &v) == nil {
			peers[event.EntityId] = &v
		}
	default:
		var v bgplsv1.Domain
		if json.Unmarshal(event.BeforeJson, &v) == nil {
			domains[event.EntityId] = &v
		}
	}
}

func (s *Store) Get(kind bgplsv1.EntityKind, id string) (proto.Message, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, v, err := s.lookupLocked(kind, id)
	if err != nil {
		return nil, err
	}
	return proto.Clone(v), nil
}

func (s *Store) Events(after, before uint64, limit int) ([]*bgplsv1.TopologyEvent, error) {
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}
	upper := prefixEnd(eventPrefix)
	if before != 0 && before != ^uint64(0) {
		upper = eventKey(before + 1)
	}
	iter, err := s.db.NewIter(&pebble.IterOptions{LowerBound: eventKey(after + 1), UpperBound: upper})
	if err != nil {
		return nil, err
	}
	defer iter.Close()
	out := make([]*bgplsv1.TopologyEvent, 0, limit)
	for iter.First(); iter.Valid() && len(out) < limit; iter.Next() {
		var event bgplsv1.TopologyEvent
		if err := json.Unmarshal(iter.Value(), &event); err != nil {
			return nil, err
		}
		out = append(out, &event)
	}
	return out, iter.Error()
}

func (s *Store) OldestRevision() uint64 {
	iter, err := s.db.NewIter(&pebble.IterOptions{LowerBound: []byte(eventPrefix), UpperBound: prefixEnd(eventPrefix)})
	if err != nil {
		return 0
	}
	defer iter.Close()
	if !iter.First() {
		return 0
	}
	n, _ := strconv.ParseUint(strings.TrimPrefix(string(iter.Key()), eventPrefix), 10, 64)
	return n
}

func (s *Store) Subscribe(ctx context.Context, after uint64) (<-chan *bgplsv1.TopologyEvent, error) {
	ch := make(chan *bgplsv1.TopologyEvent, 256)
	s.mu.Lock()
	events, err := s.Events(after, 0, 10000)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	for _, event := range events {
		ch <- event
	}
	id := s.nextSub
	s.nextSub++
	s.subs[id] = subscription{id: id, ch: ch}
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		if _, ok := s.subs[id]; ok {
			delete(s.subs, id)
			close(ch)
		}
		s.mu.Unlock()
	}()
	return ch, nil
}

func (s *Store) CompactHistory(ctx context.Context, cutoff time.Time, maxBytes uint64) error {
	iter, err := s.db.NewIter(&pebble.IterOptions{LowerBound: []byte(eventPrefix), UpperBound: prefixEnd(eventPrefix)})
	if err != nil {
		return err
	}
	defer iter.Close()
	var deleteTo []byte
	for iter.First(); iter.Valid(); iter.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var event bgplsv1.TopologyEvent
		if json.Unmarshal(iter.Value(), &event) != nil {
			continue
		}
		if event.ObservedAt.AsTime().Before(cutoff) {
			deleteTo = append([]byte(nil), iter.Key()...)
		} else {
			break
		}
	}
	if maxBytes > 0 && s.DiskUsage() > maxBytes {
		target := s.DiskUsage() - maxBytes + maxBytes/10
		var accumulated uint64
		iter.SeekGE([]byte(eventPrefix))
		for iter.Valid() {
			accumulated += uint64(len(iter.Key()) + len(iter.Value()))
			deleteTo = append([]byte(nil), iter.Key()...)
			if accumulated >= target {
				break
			}
			iter.Next()
		}
	}
	if len(deleteTo) == 0 {
		return nil
	}
	end := append(append([]byte(nil), deleteTo...), 0)
	if err := s.db.DeleteRange([]byte(eventPrefix), end, pebble.Sync); err != nil {
		return err
	}
	return s.db.Compact(ctx, []byte(eventPrefix), prefixEnd(eventPrefix), false)
}
