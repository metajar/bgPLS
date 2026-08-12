package api

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"time"

	"connectrpc.com/connect"
	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
	"github.com/bgpls/bgpls/internal/store"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gopkg.in/yaml.v3"
)

type PeerManager interface {
	Upsert(context.Context, *bgplsv1.Peer) error
	Delete(context.Context, string) error
	Reset(context.Context, string, bool) error
}
type NoopPeerManager struct{}

func (NoopPeerManager) Upsert(context.Context, *bgplsv1.Peer) error { return nil }
func (NoopPeerManager) Delete(context.Context, string) error        { return nil }
func (NoopPeerManager) Reset(context.Context, string, bool) error   { return nil }

type CollectorService struct {
	Store     *store.Store
	Peers     PeerManager
	Version   string
	StartedAt time.Time
}

func (c *CollectorService) manager() PeerManager {
	if c.Peers == nil {
		return NoopPeerManager{}
	}
	return c.Peers
}
func redactPeer(p *bgplsv1.Peer) *bgplsv1.Peer {
	out := proto.Clone(p).(*bgplsv1.Peer)
	out.TcpMd5Secret = ""
	return out
}
func validatePeer(p *bgplsv1.Peer, snap store.Snapshot) []string {
	var errs []string
	if p == nil {
		return []string{"peer is required"}
	}
	if p.Id == "" {
		errs = append(errs, "id is required")
	}
	if p.DomainId == "" {
		errs = append(errs, "domain_id is required")
	}
	foundDomain := false
	for _, d := range snap.Domains {
		if d.Id == p.DomainId {
			foundDomain = true
		}
	}
	if !foundDomain {
		errs = append(errs, "domain_id does not exist")
	}
	if _, err := netip.ParseAddr(p.RemoteAddress); err != nil {
		errs = append(errs, "remote_address must be an IPv4 or IPv6 address")
	}
	if p.LocalAddress != "" {
		if _, err := netip.ParseAddr(p.LocalAddress); err != nil {
			errs = append(errs, "local_address must be an IPv4 or IPv6 address")
		}
	}
	if p.LocalAs == 0 || p.RemoteAs == 0 {
		errs = append(errs, "local_as and remote_as must be non-zero")
	}
	if p.Gtsm && p.EbgpMultihop {
		errs = append(errs, "gtsm and ebgp_multihop cannot both be enabled")
	}
	if p.EbgpMultihop && (p.MultihopTtl < 2 || p.MultihopTtl > 255) {
		errs = append(errs, "multihop_ttl must be between 2 and 255")
	}
	return errs
}
func (c *CollectorService) GetStatus(_ context.Context, _ *connect.Request[bgplsv1.GetStatusRequest]) (*connect.Response[bgplsv1.GetStatusResponse], error) {
	return connect.NewResponse(&bgplsv1.GetStatusResponse{Version: c.Version, StartedAt: timestamppb.New(c.StartedAt), TopologyRevision: c.Store.Revision(), StorageBytes: c.Store.DiskUsage(), Ready: true, Message: "ready"}), nil
}
func (c *CollectorService) GetPeer(_ context.Context, req *connect.Request[bgplsv1.GetPeerRequest]) (*connect.Response[bgplsv1.GetPeerResponse], error) {
	v, err := c.Store.Get(bgplsv1.EntityKind_ENTITY_KIND_PEER, req.Msg.Id)
	if err != nil {
		return nil, apiError(err)
	}
	return connect.NewResponse(&bgplsv1.GetPeerResponse{Peer: redactPeer(v.(*bgplsv1.Peer))}), nil
}
func (c *CollectorService) ListPeers(_ context.Context, req *connect.Request[bgplsv1.ListPeersRequest]) (*connect.Response[bgplsv1.ListPeersResponse], error) {
	snap := c.Store.Snapshot()
	size, after := pageBounds(req.Msg.Page)
	var peers []*bgplsv1.Peer
	for _, p := range snap.Peers {
		if p.Id <= after || (req.Msg.DomainId != "" && p.DomainId != req.Msg.DomainId) {
			continue
		}
		peers = append(peers, redactPeer(p))
		if len(peers) > size {
			break
		}
	}
	more := len(peers) > size
	if more {
		peers = peers[:size]
	}
	token := ""
	if len(peers) > 0 {
		token = nextToken(peers[len(peers)-1].Id, more)
	}
	return connect.NewResponse(&bgplsv1.ListPeersResponse{Peers: peers, Page: &bgplsv1.PageResult{NextPageToken: token, Revision: snap.Revision, ObservedAt: timestamppb.New(snap.Observed)}}), nil
}
func (c *CollectorService) CreatePeer(ctx context.Context, req *connect.Request[bgplsv1.CreatePeerRequest]) (*connect.Response[bgplsv1.CreatePeerResponse], error) {
	p := req.Msg.Peer
	if errs := validatePeer(p, c.Store.Snapshot()); len(errs) > 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(fmt.Sprint(errs)))
	}
	if _, err := c.Store.Get(bgplsv1.EntityKind_ENTITY_KIND_PEER, p.Id); err == nil {
		return nil, connect.NewError(connect.CodeAlreadyExists, errors.New("peer already exists"))
	}
	p = proto.Clone(p).(*bgplsv1.Peer)
	p.SessionState = bgplsv1.PeerSessionState_PEER_SESSION_STATE_IDLE
	if !p.Enabled {
		p.SessionState = bgplsv1.PeerSessionState_PEER_SESSION_STATE_DISABLED
	}
	if _, err := c.Store.Apply(ctx, store.Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_PEER, ID: p.Id, DomainID: p.DomainId, Value: p, Reason: "peer created"}); err != nil {
		return nil, apiError(err)
	}
	if err := c.manager().Upsert(ctx, p); err != nil {
		return nil, apiError(err)
	}
	return connect.NewResponse(&bgplsv1.CreatePeerResponse{Peer: redactPeer(p)}), nil
}
func (c *CollectorService) UpdatePeer(ctx context.Context, req *connect.Request[bgplsv1.UpdatePeerRequest]) (*connect.Response[bgplsv1.UpdatePeerResponse], error) {
	p := req.Msg.Peer
	if errs := validatePeer(p, c.Store.Snapshot()); len(errs) > 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New(fmt.Sprint(errs)))
	}
	existingRaw, err := c.Store.Get(bgplsv1.EntityKind_ENTITY_KIND_PEER, p.Id)
	if err != nil {
		return nil, apiError(err)
	}
	existing := existingRaw.(*bgplsv1.Peer)
	if req.Msg.ExpectedResourceVersion != existing.ResourceVersion {
		return nil, connect.NewError(connect.CodeAborted, fmt.Errorf("resource version mismatch: current is %d", existing.ResourceVersion))
	}
	p = proto.Clone(p).(*bgplsv1.Peer)
	if p.TcpMd5Secret == "" {
		p.TcpMd5Secret = existing.TcpMd5Secret
	}
	p.SessionState = existing.SessionState
	p.ReceivedUpdates = existing.ReceivedUpdates
	p.RejectedUpdates = existing.RejectedUpdates
	if _, err := c.Store.Apply(ctx, store.Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_PEER, ID: p.Id, DomainID: p.DomainId, Value: p, Reason: "peer updated"}); err != nil {
		return nil, apiError(err)
	}
	if err := c.manager().Upsert(ctx, p); err != nil {
		return nil, apiError(err)
	}
	return connect.NewResponse(&bgplsv1.UpdatePeerResponse{Peer: redactPeer(p)}), nil
}
func (c *CollectorService) DeletePeer(ctx context.Context, req *connect.Request[bgplsv1.DeletePeerRequest]) (*connect.Response[bgplsv1.DeletePeerResponse], error) {
	raw, err := c.Store.Get(bgplsv1.EntityKind_ENTITY_KIND_PEER, req.Msg.Id)
	if err != nil {
		return nil, apiError(err)
	}
	p := raw.(*bgplsv1.Peer)
	if req.Msg.ExpectedResourceVersion != p.ResourceVersion {
		return nil, connect.NewError(connect.CodeAborted, errors.New("resource version mismatch"))
	}
	if err := c.manager().Delete(ctx, p.Id); err != nil {
		return nil, apiError(err)
	}
	if _, err := c.Store.Apply(ctx, store.Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_PEER, ID: p.Id, DomainID: p.DomainId, Delete: true, Reason: "peer deleted"}); err != nil {
		return nil, apiError(err)
	}
	return connect.NewResponse(&bgplsv1.DeletePeerResponse{}), nil
}
func (c *CollectorService) SetPeerAdminState(ctx context.Context, req *connect.Request[bgplsv1.SetPeerAdminStateRequest]) (*connect.Response[bgplsv1.SetPeerAdminStateResponse], error) {
	raw, err := c.Store.Get(bgplsv1.EntityKind_ENTITY_KIND_PEER, req.Msg.Id)
	if err != nil {
		return nil, apiError(err)
	}
	p := raw.(*bgplsv1.Peer)
	if req.Msg.ExpectedResourceVersion != p.ResourceVersion {
		return nil, connect.NewError(connect.CodeAborted, errors.New("resource version mismatch"))
	}
	p.Enabled = req.Msg.Enabled
	if !p.Enabled {
		p.SessionState = bgplsv1.PeerSessionState_PEER_SESSION_STATE_DISABLED
	}
	if _, err := c.Store.Apply(ctx, store.Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_PEER, ID: p.Id, DomainID: p.DomainId, Value: p, Reason: "peer administrative state changed"}); err != nil {
		return nil, apiError(err)
	}
	if err := c.manager().Upsert(ctx, p); err != nil {
		return nil, apiError(err)
	}
	return connect.NewResponse(&bgplsv1.SetPeerAdminStateResponse{Peer: redactPeer(p)}), nil
}
func (c *CollectorService) ResetPeer(ctx context.Context, req *connect.Request[bgplsv1.ResetPeerRequest]) (*connect.Response[bgplsv1.ResetPeerResponse], error) {
	raw, err := c.Store.Get(bgplsv1.EntityKind_ENTITY_KIND_PEER, req.Msg.Id)
	if err != nil {
		return nil, apiError(err)
	}
	if err := c.manager().Reset(ctx, req.Msg.Id, req.Msg.Soft); err != nil {
		return nil, apiError(err)
	}
	return connect.NewResponse(&bgplsv1.ResetPeerResponse{Peer: redactPeer(raw.(*bgplsv1.Peer))}), nil
}
func (c *CollectorService) ValidatePeerConfig(_ context.Context, req *connect.Request[bgplsv1.ValidatePeerConfigRequest]) (*connect.Response[bgplsv1.ValidatePeerConfigResponse], error) {
	errs := validatePeer(req.Msg.Peer, c.Store.Snapshot())
	return connect.NewResponse(&bgplsv1.ValidatePeerConfigResponse{Valid: len(errs) == 0, Errors: errs}), nil
}

type peerExport struct {
	Peers []*bgplsv1.Peer `yaml:"peers"`
}

func (c *CollectorService) ExportPeerConfig(_ context.Context, _ *connect.Request[bgplsv1.ExportPeerConfigRequest]) (*connect.Response[bgplsv1.ExportPeerConfigResponse], error) {
	snap := c.Store.Snapshot()
	peers := make([]*bgplsv1.Peer, 0, len(snap.Peers))
	for _, p := range snap.Peers {
		peers = append(peers, redactPeer(p))
	}
	data, err := yaml.Marshal(peerExport{Peers: peers})
	if err != nil {
		return nil, apiError(err)
	}
	return connect.NewResponse(&bgplsv1.ExportPeerConfigResponse{Yaml: data}), nil
}
func (c *CollectorService) ImportPeerConfig(ctx context.Context, req *connect.Request[bgplsv1.ImportPeerConfigRequest]) (*connect.Response[bgplsv1.ImportPeerConfigResponse], error) {
	var input peerExport
	if err := yaml.Unmarshal(req.Msg.Yaml, &input); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	snap := c.Store.Snapshot()
	for _, p := range input.Peers {
		if errs := validatePeer(p, snap); len(errs) > 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("peer %s: %v", p.Id, errs))
		}
	}
	incoming := map[string]bool{}
	for _, p := range input.Peers {
		incoming[p.Id] = true
		if raw, err := c.Store.Get(bgplsv1.EntityKind_ENTITY_KIND_PEER, p.Id); err == nil && p.TcpMd5Secret == "" {
			p.TcpMd5Secret = raw.(*bgplsv1.Peer).TcpMd5Secret
		}
		if _, err := c.Store.Apply(ctx, store.Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_PEER, ID: p.Id, DomainID: p.DomainId, Value: p, Reason: "peer configuration imported"}); err != nil {
			return nil, apiError(err)
		}
		if err := c.manager().Upsert(ctx, p); err != nil {
			return nil, apiError(err)
		}
	}
	if req.Msg.Replace {
		for _, p := range snap.Peers {
			if !incoming[p.Id] {
				if err := c.manager().Delete(ctx, p.Id); err != nil {
					return nil, apiError(err)
				}
				if _, err := c.Store.Apply(ctx, store.Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_PEER, ID: p.Id, DomainID: p.DomainId, Delete: true, Reason: "peer removed by configuration import"}); err != nil {
					return nil, apiError(err)
				}
			}
		}
	}
	sort.Slice(input.Peers, func(i, j int) bool { return input.Peers[i].Id < input.Peers[j].Id })
	for i := range input.Peers {
		input.Peers[i] = redactPeer(input.Peers[i])
	}
	return connect.NewResponse(&bgplsv1.ImportPeerConfigResponse{Peers: input.Peers}), nil
}
