package api

import (
	"context"
	"time"

	"connectrpc.com/connect"
	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
	"github.com/bgpls/bgpls/internal/store"
	"github.com/bgpls/bgpls/internal/utilization"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type EnrichmentService struct {
	Store   *store.Store
	Overlay *utilization.Overlay
}

func (s *EnrichmentService) overlay() *utilization.Overlay {
	if s == nil {
		return nil
	}
	return s.Overlay
}

func (s *EnrichmentService) ReportInterfaceUtilization(ctx context.Context, req *connect.Request[bgplsv1.ReportInterfaceUtilizationRequest]) (*connect.Response[bgplsv1.ReportInterfaceUtilizationResponse], error) {
	o := s.overlay()
	if o == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errOverlayMissing)
	}
	if s.Store != nil {
		o.RebuildIndex(s.Store.Snapshot().Links)
	}
	role := roleLabel(RoleFrom(ctx))
	stats := o.Report(time.Now().UTC(), role, req.Msg.Interfaces)
	return connect.NewResponse(&bgplsv1.ReportInterfaceUtilizationResponse{
		Accepted:     stats.Accepted,
		Correlated:   stats.Correlated,
		Uncorrelated: stats.Uncorrelated,
	}), nil
}

func (s *EnrichmentService) GetLinkUtilization(_ context.Context, req *connect.Request[bgplsv1.GetLinkUtilizationRequest]) (*connect.Response[bgplsv1.GetLinkUtilizationResponse], error) {
	o := s.overlay()
	if o == nil {
		return connect.NewResponse(&bgplsv1.GetLinkUtilizationResponse{ObservedAt: timestamppb.Now()}), nil
	}
	links := o.List(req.Msg.LinkIds)
	if req.Msg.DomainId != "" {
		filtered := links[:0]
		for _, l := range links {
			filtered = append(filtered, l)
		}
		links = filtered
	}
	return connect.NewResponse(&bgplsv1.GetLinkUtilizationResponse{Links: links, ObservedAt: timestamppb.Now()}), nil
}

func (s *EnrichmentService) WatchLinkUtilization(ctx context.Context, req *connect.Request[bgplsv1.WatchLinkUtilizationRequest], stream *connect.ServerStream[bgplsv1.WatchLinkUtilizationResponse]) error {
	o := s.overlay()
	if o == nil {
		return connect.NewError(connect.CodeFailedPrecondition, errOverlayMissing)
	}
	events, err := o.Subscribe(ctx, req.Msg.LinkIds)
	if err != nil {
		return apiError(err)
	}
	for event := range events {
		if err := stream.Send(event); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (s *EnrichmentService) GetUncorrelatedInterfaces(context.Context, *connect.Request[bgplsv1.GetUncorrelatedInterfacesRequest]) (*connect.Response[bgplsv1.GetUncorrelatedInterfacesResponse], error) {
	o := s.overlay()
	if o == nil {
		return connect.NewResponse(&bgplsv1.GetUncorrelatedInterfacesResponse{}), nil
	}
	return connect.NewResponse(&bgplsv1.GetUncorrelatedInterfacesResponse{Interfaces: o.Uncorrelated()}), nil
}
