package store

import (
	"context"
	"testing"

	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
)

func TestApplyRecoverAndSnapshotAt(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	node := &bgplsv1.Node{Meta: &bgplsv1.EntityMeta{Id: "n1", DomainId: "d1"}, Name: "before"}
	first, err := s.Apply(ctx, Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_NODE, ID: "n1", DomainID: "d1", Value: node})
	if err != nil {
		t.Fatal(err)
	}
	node.Name = "after"
	second, err := s.Apply(ctx, Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_NODE, ID: "n1", DomainID: "d1", Value: node})
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != first.Revision+1 {
		t.Fatalf("revisions are not monotonic: %d %d", first.Revision, second.Revision)
	}
	historical, err := s.SnapshotAt(first.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if got := historical.Nodes[0].Name; got != "before" {
		t.Fatalf("historical name = %q", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snap := s.Snapshot()
	if snap.Revision != second.Revision || snap.Observed.IsZero() || len(snap.Nodes) != 1 || snap.Nodes[0].Name != "after" {
		t.Fatalf("unexpected recovered snapshot: %+v", snap)
	}
}

func TestSubscribeReplaysAndStreams(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = s.Apply(ctx, Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_NODE, ID: "n1", Value: &bgplsv1.Node{Meta: &bgplsv1.EntityMeta{Id: "n1"}}})
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.Subscribe(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := <-events; got.Revision != 1 {
		t.Fatalf("replayed revision = %d", got.Revision)
	}
	_, err = s.Apply(ctx, Mutation{Kind: bgplsv1.EntityKind_ENTITY_KIND_NODE, ID: "n2", Value: &bgplsv1.Node{Meta: &bgplsv1.EntityMeta{Id: "n2"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := <-events; got.Revision != 2 {
		t.Fatalf("streamed revision = %d", got.Revision)
	}
}
