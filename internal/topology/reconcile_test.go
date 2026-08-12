package topology

import (
	"context"
	"testing"

	bgplsv1 "github.com/bgpls/bgpls/gen/bgpls/v1"
	"github.com/bgpls/bgpls/internal/store"
)

func TestReconcilerPreferenceAndWithdrawal(t *testing.T) {
	s, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r := NewReconciler(s)
	ctx := context.Background()
	low := &bgplsv1.Node{Meta: &bgplsv1.EntityMeta{Id: "n", DomainId: "d"}, Name: "low", BgpRouterId: "192.0.2.1"}
	high := &bgplsv1.Node{Meta: &bgplsv1.EntityMeta{Id: "n", DomainId: "d"}, Name: "high"}
	if _, err := r.Apply(ctx, Advertisement{Kind: bgplsv1.EntityKind_ENTITY_KIND_NODE, ID: "n", DomainID: "d", PeerID: "low", Preference: 10, Value: low}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Apply(ctx, Advertisement{Kind: bgplsv1.EntityKind_ENTITY_KIND_NODE, ID: "n", DomainID: "d", PeerID: "high", Preference: 100, Value: high}); err != nil {
		t.Fatal(err)
	}
	raw, _ := s.Get(bgplsv1.EntityKind_ENTITY_KIND_NODE, "n")
	canonical := raw.(*bgplsv1.Node)
	if canonical.Name != "high" {
		t.Fatal("higher-preference source was not selected")
	}
	if canonical.BgpRouterId != "192.0.2.1" {
		t.Fatal("missing metadata was not filled from lower-preference source")
	}
	if len(canonical.Meta.Conflicts) != 1 {
		t.Fatalf("expected one source conflict, got %d", len(canonical.Meta.Conflicts))
	}
	if _, err := r.Apply(ctx, Advertisement{Kind: bgplsv1.EntityKind_ENTITY_KIND_NODE, ID: "n", DomainID: "d", PeerID: "high", Withdraw: true}); err != nil {
		t.Fatal(err)
	}
	raw, _ = s.Get(bgplsv1.EntityKind_ENTITY_KIND_NODE, "n")
	if raw.(*bgplsv1.Node).Name != "low" {
		t.Fatal("withdraw did not reveal fallback source")
	}
}
