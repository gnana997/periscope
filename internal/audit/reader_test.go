package audit

import (
	"context"
	"testing"
	"time"
)

// seed populates the sink with a deterministic mix of events for
// query tests. Returns the timestamps used so assertions can refer
// to them without re-deriving.
func seed(t *testing.T, s *SQLiteSink) (older, newer time.Time) {
	t.Helper()
	older = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	newer = time.Date(2030, 1, 10, 0, 0, 0, 0, time.UTC)
	ctx := context.Background()

	events := []Event{
		// Alice — apply success in prod
		{Timestamp: older, Actor: Actor{Sub: "alice"}, Verb: VerbApply,
			Outcome: OutcomeSuccess, Cluster: "prod",
			Resource: ResourceRef{Resource: "deployments", Namespace: "shop", Name: "checkout"}},
		// Alice — delete denied in prod
		{Timestamp: newer, Actor: Actor{Sub: "alice"}, Verb: VerbDelete,
			Outcome: OutcomeDenied, Cluster: "prod",
			Resource: ResourceRef{Resource: "deployments", Namespace: "shop", Name: "checkout"},
			Reason:  "forbidden"},
		// Bob — secret reveal success in stage
		{Timestamp: newer, Actor: Actor{Sub: "bob"}, Verb: VerbSecretReveal,
			Outcome: OutcomeSuccess, Cluster: "stage",
			Resource: ResourceRef{Resource: "secrets", Namespace: "ops", Name: "creds"},
			Extra:   map[string]any{"key": "password"}},
		// Bob — apply failure in prod
		{Timestamp: newer, Actor: Actor{Sub: "bob"}, Verb: VerbApply,
			Outcome: OutcomeFailure, Cluster: "prod", RequestID: "req-bob-1",
			Resource: ResourceRef{Resource: "deployments", Namespace: "ops", Name: "api"},
			Reason:  "apply: parse yaml: bad"},
	}
	for _, e := range events {
		s.Record(ctx, e)
	}
	return older, newer
}

func TestQuery_NoFiltersReturnsAllNewestFirst(t *testing.T) {
	s := openTestSink(t, SQLiteConfig{})
	older, newer := seed(t, s)

	got, err := s.Query(context.Background(), QueryArgs{})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if got.Total != 4 {
		t.Errorf("Total: got %d, want 4", got.Total)
	}
	if len(got.Items) != 4 {
		t.Fatalf("Items: got %d, want 4", len(got.Items))
	}
	// First three rows share `newer`; last row is `older`.
	if !got.Items[3].Timestamp.Equal(older) {
		t.Errorf("oldest row not last: got %v, want %v", got.Items[3].Timestamp, older)
	}
	for _, r := range got.Items[:3] {
		if !r.Timestamp.Equal(newer) {
			t.Errorf("expected newer ts, got %v", r.Timestamp)
		}
	}
}

func TestQuery_FilterByActor(t *testing.T) {
	s := openTestSink(t, SQLiteConfig{})
	seed(t, s)

	got, _ := s.Query(context.Background(), QueryArgs{Actor: "bob"})
	if got.Total != 2 {
		t.Errorf("bob total: got %d, want 2", got.Total)
	}
	for _, r := range got.Items {
		if r.Actor.Sub != "bob" {
			t.Errorf("non-bob row leaked: %+v", r.Actor)
		}
	}
}

func TestQuery_FilterByVerbAndOutcome(t *testing.T) {
	s := openTestSink(t, SQLiteConfig{})
	seed(t, s)

	got, _ := s.Query(context.Background(), QueryArgs{
		Verb:    VerbApply,
		Outcome: OutcomeFailure,
	})
	if got.Total != 1 {
		t.Errorf("apply+failure: got %d, want 1", got.Total)
	}
	if got.Items[0].Actor.Sub != "bob" {
		t.Errorf("expected bob, got %q", got.Items[0].Actor.Sub)
	}
}

func TestQuery_FilterByClusterNamespace(t *testing.T) {
	s := openTestSink(t, SQLiteConfig{})
	seed(t, s)

	got, _ := s.Query(context.Background(), QueryArgs{
		Cluster:   "prod",
		Namespace: "shop",
	})
	if got.Total != 2 {
		t.Errorf("prod+shop: got %d, want 2", got.Total)
	}
}

func TestQuery_FilterByRequestID(t *testing.T) {
	s := openTestSink(t, SQLiteConfig{})
	seed(t, s)

	got, _ := s.Query(context.Background(), QueryArgs{RequestID: "req-bob-1"})
	if got.Total != 1 {
		t.Fatalf("by request_id: got %d, want 1", got.Total)
	}
	if got.Items[0].RequestID != "req-bob-1" {
		t.Errorf("request_id mismatch: %q", got.Items[0].RequestID)
	}
}

func TestQuery_FilterByTimeRange(t *testing.T) {
	s := openTestSink(t, SQLiteConfig{})
	older, newer := seed(t, s)

	// From after `older` -> only the three `newer` rows.
	got, _ := s.Query(context.Background(), QueryArgs{
		From: older.Add(time.Second),
	})
	if got.Total != 3 {
		t.Errorf("from-after-older: got %d, want 3", got.Total)
	}
	// To before `newer` -> only the one `older` row.
	got, _ = s.Query(context.Background(), QueryArgs{
		To: newer,
	})
	if got.Total != 1 {
		t.Errorf("to-before-newer: got %d, want 1", got.Total)
	}
}

func TestQuery_PaginationAndLimitClamp(t *testing.T) {
	s := openTestSink(t, SQLiteConfig{})
	seed(t, s)

	page1, _ := s.Query(context.Background(), QueryArgs{Limit: 2, Offset: 0})
	if page1.Limit != 2 || page1.Offset != 0 || len(page1.Items) != 2 {
		t.Errorf("page1: limit=%d offset=%d items=%d", page1.Limit, page1.Offset, len(page1.Items))
	}
	page2, _ := s.Query(context.Background(), QueryArgs{Limit: 2, Offset: 2})
	if len(page2.Items) != 2 {
		t.Errorf("page2 items: got %d, want 2", len(page2.Items))
	}
	if page1.Items[0].ID == page2.Items[0].ID {
		t.Errorf("page1 and page2 overlap on id")
	}

	// Limit=0 => DefaultQueryLimit.
	got, _ := s.Query(context.Background(), QueryArgs{Limit: 0})
	if got.Limit != DefaultQueryLimit {
		t.Errorf("default limit: got %d, want %d", got.Limit, DefaultQueryLimit)
	}
	// Limit > Max => clamp to Max.
	got, _ = s.Query(context.Background(), QueryArgs{Limit: MaxQueryLimit + 100})
	if got.Limit != MaxQueryLimit {
		t.Errorf("max limit: got %d, want %d", got.Limit, MaxQueryLimit)
	}
}

func TestQuery_ExtraJSONRoundTrips(t *testing.T) {
	s := openTestSink(t, SQLiteConfig{})
	seed(t, s)

	got, _ := s.Query(context.Background(), QueryArgs{Verb: VerbSecretReveal})
	if got.Total != 1 {
		t.Fatalf("secret_reveal total: got %d", got.Total)
	}
	if got.Items[0].Extra["key"] != "password" {
		t.Errorf("Extra.key: got %v", got.Items[0].Extra["key"])
	}
}
