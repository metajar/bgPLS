package gnmi

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bgpls/bgpls/internal/utilization"
	gnmipb "github.com/openconfig/gnmi/proto/gnmi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

type Config struct {
	Name               string
	Address            string
	Username           string
	Password           string
	SampleInterval     time.Duration
	Insecure           bool
	InsecureSkipVerify bool
}

type Collector struct {
	cfg Config
}

func New(cfg Config) (*Collector, error) {
	if cfg.Name == "" || cfg.Address == "" {
		return nil, fmt.Errorf("gnmi collector requires name and address")
	}
	if cfg.SampleInterval <= 0 {
		cfg.SampleInterval = 10 * time.Second
	}
	return &Collector{cfg: cfg}, nil
}

func (c *Collector) Describe() string {
	return "gnmi:" + c.cfg.Name + ":" + c.cfg.Address
}

func (c *Collector) Run(ctx context.Context, out chan<- utilization.InterfaceSample) error {
	backoff := time.Second
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		err := c.subscribe(ctx, out)
		if ctx.Err() != nil {
			return nil
		}
		slog.Warn("gNMI subscription ended, reconnecting", "target", c.cfg.Name, "error", err)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

func (c *Collector) subscribe(ctx context.Context, out chan<- utilization.InterfaceSample) error {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(dialCtx, c.cfg.Address, c.dialOptions()...)
	if err != nil {
		return err
	}
	defer conn.Close()
	client := gnmipb.NewGNMIClient(conn)
	md := metadata.Pairs("username", c.cfg.Username, "password", c.cfg.Password)
	stream, err := client.Subscribe(metadata.NewOutgoingContext(ctx, md))
	if err != nil {
		return err
	}
	ns := uint64(c.cfg.SampleInterval.Nanoseconds())
	req := &gnmipb.SubscribeRequest{Request: &gnmipb.SubscribeRequest_Subscribe{Subscribe: &gnmipb.SubscriptionList{
		Mode:     gnmipb.SubscriptionList_STREAM,
		Encoding: gnmipb.Encoding_JSON_IETF,
		Subscription: []*gnmipb.Subscription{
			sampleSub([]string{"interface"}, "statistics", "in-octets", ns),
			sampleSub([]string{"interface"}, "statistics", "out-octets", ns),
			sampleSub([]string{"interface", "ethernet"}, "port-speed", "", ns),
			onchangeSub([]string{"interface", "subinterface", "ipv4"}, "address"),
			onchangeSub([]string{"interface", "subinterface", "ipv6"}, "address"),
		},
	}}}
	if err := stream.Send(req); err != nil {
		return err
	}
	state := map[string]*ifaceState{}
	var mu sync.Mutex
	for {
		resp, err := stream.Recv()
		if err != nil {
			return err
		}
		notif := resp.GetUpdate()
		if notif == nil {
			continue
		}
		ts := time.Unix(0, notif.Timestamp)
		if ts.IsZero() || notif.Timestamp == 0 {
			ts = time.Now().UTC()
		}
		prefixIface, _, _ := pathString(notif.Prefix)
		mu.Lock()
		for _, upd := range notif.Update {
			c.applyUpdate(state, prefixIface, upd, ts, out)
		}
		mu.Unlock()
	}
}

func (c *Collector) applyUpdate(state map[string]*ifaceState, prefixIface string, upd *gnmipb.Update, ts time.Time, out chan<- utilization.InterfaceSample) {
	if upd == nil {
		return
	}
	iface, leaf, keys := pathString(upd.Path)
	if iface == "" {
		iface = prefixIface
	}
	if iface == "" || iface == "*" {
		return
	}
	st := state[iface]
	if st == nil {
		st = &ifaceState{name: iface}
		state[iface] = st
	}
	switch leaf {
	case "in-octets":
		if n, ok := parseUint(upd.Val); ok {
			st.inOctets = n
			st.haveIn = true
			c.emit(st, ts, out)
		}
	case "out-octets":
		if n, ok := parseUint(upd.Val); ok {
			st.outOctets = n
			st.haveOut = true
			c.emit(st, ts, out)
		}
	case "port-speed":
		st.speedBps = mapPortSpeed(parseString(upd.Val))
	case "ip-prefix", "address":
		addr := parseString(upd.Val)
		if addr == "" {
			addr = keys["ip-prefix"]
		}
		if strings.Contains(pathJoin(upd.Path), "ipv6") {
			st.ipv6 = addUnique(st.ipv6, addr)
		} else {
			st.ipv4 = addUnique(st.ipv4, addr)
		}
	}
}

func pathJoin(p *gnmipb.Path) string {
	if p == nil {
		return ""
	}
	var b strings.Builder
	for _, e := range p.Elem {
		b.WriteByte('/')
		b.WriteString(e.Name)
	}
	return b.String()
}

func (c *Collector) emit(st *ifaceState, ts time.Time, out chan<- utilization.InterfaceSample) {
	if !st.haveIn && !st.haveOut {
		return
	}
	sample := utilization.InterfaceSample{
		Device:        c.cfg.Name,
		InterfaceName: st.name,
		IPv4Addrs:     append([]string(nil), st.ipv4...),
		IPv6Addrs:     append([]string(nil), st.ipv6...),
		SpeedBps:      st.speedBps,
		InOctets:      st.inOctets,
		OutOctets:     st.outOctets,
		Timestamp:     ts,
	}
	select {
	case out <- sample:
	default:
	}
}

func (c *Collector) dialOptions() []grpc.DialOption {
	if c.cfg.Insecure {
		return []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	}
	tlsCfg := &tls.Config{InsecureSkipVerify: c.cfg.InsecureSkipVerify, MinVersion: tls.VersionTLS12}
	return []grpc.DialOption{grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg))}
}

func sampleSub(parents []string, mid, leaf string, interval uint64) *gnmipb.Subscription {
	elems := make([]*gnmipb.PathElem, 0, len(parents)+2)
	for i, name := range parents {
		elem := &gnmipb.PathElem{Name: name}
		if i == 0 && name == "interface" {
			elem.Key = map[string]string{"name": "*"}
		}
		if name == "subinterface" {
			elem.Key = map[string]string{"index": "*"}
		}
		elems = append(elems, elem)
	}
	if mid != "" && mid != parents[len(parents)-1] {
		elems = append(elems, &gnmipb.PathElem{Name: mid})
	}
	if leaf != "" {
		elems = append(elems, &gnmipb.PathElem{Name: leaf})
	}
	return &gnmipb.Subscription{
		Path:              &gnmipb.Path{Elem: elems},
		Mode:              gnmipb.SubscriptionMode_SAMPLE,
		SampleInterval:    interval,
		SuppressRedundant: false,
	}
}

func onchangeSub(parents []string, leaf string) *gnmipb.Subscription {
	elems := make([]*gnmipb.PathElem, 0, len(parents)+2)
	for _, name := range parents {
		elem := &gnmipb.PathElem{Name: name}
		if name == "interface" {
			elem.Key = map[string]string{"name": "*"}
		}
		if name == "subinterface" {
			elem.Key = map[string]string{"index": "*"}
		}
		elems = append(elems, elem)
	}
	if leaf != "" {
		elems = append(elems, &gnmipb.PathElem{Name: leaf})
	}
	return &gnmipb.Subscription{Path: &gnmipb.Path{Elem: elems}, Mode: gnmipb.SubscriptionMode_ON_CHANGE}
}
