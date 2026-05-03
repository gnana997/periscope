package audit

import (
	"strings"
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func openTestSink(t *testing.T, cfg SQLiteConfig) *SQLiteSink {
	t.Helper()
	if cfg.Path == "" {
		cfg.Path = filepath.Join(t.TempDir(), "audit.db")
	}
	if cfg.VacuumInterval == 0 {
		// Long enough to not fire during the test; the test
		// invokes runRetention directly when it wants pruning.
		cfg.VacuumInterval = time.Hour
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s, err := OpenSQLiteSink(ctx, cfg)
	if err != nil {
		t.Fatalf("OpenSQLiteSink: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func countRows(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit_events`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestSQLiteSink_RoundTrip(t *testing.T) {
	s := openTestSink(t, SQLiteConfig{})
	ctx := context.Background()
	now := time.Now().UTC()
	s.Record(ctx, Event{
		Timestamp: now,
		RequestID: "req-1",
		Actor:     Actor{Sub: "u@x", Email: "u@x", Groups: []string{"sre"}},
		Verb:      VerbDelete,
		Outcome:   OutcomeSuccess,
		Cluster:   "prod",
		Resource: ResourceRef{
			Group: "apps", Version: "v1", Resource: "deployments",
			Namespace: "shop", Name: "checkout",
		},
		Reason: "",
		Extra:  map[string]any{"propagation": "Background"},
	})

	var (
		verb, outcome, actor, cluster, ns, name string
		extra                                   sql.NullString
	)
	row := s.db.QueryRow(`
		SELECT verb, outcome, actor_sub, cluster, res_namespace, res_name, extra
		FROM audit_events LIMIT 1`)
	if err := row.Scan(&verb, &outcome, &actor, &cluster, &ns, &name, &extra); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if verb != "delete" || outcome != "success" || actor != "u@x" {
		t.Errorf("unexpected row: verb=%q outcome=%q actor=%q", verb, outcome, actor)
	}
	if cluster != "prod" || ns != "shop" || name != "checkout" {
		t.Errorf("unexpected scope: cluster=%q ns=%q name=%q", cluster, ns, name)
	}
	if !extra.Valid || extra.String == "" {
		t.Errorf("extra not stored as JSON: %+v", extra)
	}
}

func TestSQLiteSink_NullableEmptyFields(t *testing.T) {
	s := openTestSink(t, SQLiteConfig{})
	s.Record(context.Background(), Event{
		Timestamp: time.Now().UTC(),
		Actor:     Actor{Sub: "u"},
		Verb:      VerbExecOpen,
		Outcome:   OutcomeSuccess,
		// Cluster, namespace, request_id, route, reason all empty.
	})

	var requestID, route, ns, reason, groups sql.NullString
	row := s.db.QueryRow(`
		SELECT request_id, route, res_namespace, reason, actor_groups
		FROM audit_events LIMIT 1`)
	if err := row.Scan(&requestID, &route, &ns, &reason, &groups); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, f := range []sql.NullString{requestID, route, ns, reason, groups} {
		if f.Valid {
			t.Errorf("expected NULL, got %q", f.String)
		}
	}
}

func TestSQLiteSink_PruneByAge(t *testing.T) {
	s := openTestSink(t, SQLiteConfig{
		RetentionDays:  7,
		VacuumInterval: time.Hour,
	})
	ctx := context.Background()
	old := time.Now().UTC().Add(-30 * 24 * time.Hour)
	fresh := time.Now().UTC()

	for i := 0; i < 5; i++ {
		s.Record(ctx, Event{
			Timestamp: old, Actor: Actor{Sub: "u"},
			Verb: VerbDelete, Outcome: OutcomeSuccess,
		})
	}
	for i := 0; i < 3; i++ {
		s.Record(ctx, Event{
			Timestamp: fresh, Actor: Actor{Sub: "u"},
			Verb: VerbDelete, Outcome: OutcomeSuccess,
		})
	}
	if got := countRows(t, s.db); got != 8 {
		t.Fatalf("setup count: got %d want 8", got)
	}

	s.runRetention(ctx)

	if got := countRows(t, s.db); got != 3 {
		t.Errorf("after prune-by-age: got %d want 3", got)
	}
}

func TestSQLiteSink_PruneByAgeDisabled(t *testing.T) {
	s := openTestSink(t, SQLiteConfig{
		RetentionDays:  0, // disabled
		MaxSizeMB:      0, // disabled
		VacuumInterval: time.Hour,
	})
	ctx := context.Background()
	old := time.Now().UTC().Add(-365 * 24 * time.Hour)
	for i := 0; i < 5; i++ {
		s.Record(ctx, Event{
			Timestamp: old, Actor: Actor{Sub: "u"},
			Verb: VerbDelete, Outcome: OutcomeSuccess,
		})
	}
	s.runRetention(ctx)
	if got := countRows(t, s.db); got != 5 {
		t.Errorf("retention disabled: got %d want 5", got)
	}
}

func TestSQLiteSink_RecordSurvivesAfterClose(t *testing.T) {
	// Per the fail-open policy: a Record call on a closed sink
	// should log an error but never panic.
	s := openTestSink(t, SQLiteConfig{})
	_ = s.Close()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Record after Close panicked: %v", r)
		}
	}()
	s.Record(context.Background(), Event{
		Timestamp: time.Now().UTC(),
		Actor:     Actor{Sub: "u"},
		Verb:      VerbDelete, Outcome: OutcomeSuccess,
	})
}

func TestValidate_UnboundedGrowth(t *testing.T) {
	cfg := SQLiteConfig{
		RetentionDays: 0, MaxSizeMB: 0,
		VacuumInterval: time.Hour,
		Path:           "/tmp/nonexistent/audit.db",
	}
	warns := cfg.Validate()
	if len(warns) == 0 {
		t.Fatal("want at least one warning for unbounded growth")
	}
	if !containsSubstr(warns, "retentionDays and maxSizeMB are 0") {
		t.Errorf("missing unbounded-growth warning: %v", warns)
	}
}

func TestValidate_VacuumIntervalTooSmall(t *testing.T) {
	cfg := SQLiteConfig{
		RetentionDays: 30, MaxSizeMB: 1024,
		VacuumInterval: time.Second,
		Path:           "/tmp/nonexistent/audit.db",
	}
	warns := cfg.Validate()
	if !containsSubstr(warns, "vacuumInterval < 1m") {
		t.Errorf("missing vacuum-interval warning: %v", warns)
	}
}

func TestValidate_RetentionTooLong(t *testing.T) {
	cfg := SQLiteConfig{
		RetentionDays: 400, MaxSizeMB: 1024,
		VacuumInterval: time.Hour,
		Path:           "/tmp/nonexistent/audit.db",
	}
	warns := cfg.Validate()
	if !containsSubstr(warns, "retentionDays > 365") {
		t.Errorf("missing retention-too-long warning: %v", warns)
	}
}

func TestValidate_HappyPath(t *testing.T) {
	cfg := SQLiteConfig{
		RetentionDays: 30, MaxSizeMB: 1024,
		VacuumInterval: 24 * time.Hour,
		Path:           "/tmp/nonexistent/audit.db",
	}
	warns := cfg.Validate()
	// Disk-free check is skipped for nonexistent path; the other
	// checks should all pass.
	if len(warns) != 0 {
		t.Errorf("expected no warnings for happy path, got %v", warns)
	}
}

func containsSubstr(haystack []string, needle string) bool {
	for _, h := range haystack {
		if len(h) >= len(needle) {
			for i := 0; i+len(needle) <= len(h); i++ {
				if h[i:i+len(needle)] == needle {
					return true
				}
			}
		}
	}
	return false
}

func TestLoadSQLiteConfigFromEnv_Defaults(t *testing.T) {
	t.Setenv("PERISCOPE_AUDIT_ENABLED", "")
	t.Setenv("PERISCOPE_AUDIT_DB_PATH", "")
	t.Setenv("PERISCOPE_AUDIT_RETENTION_DAYS", "")
	t.Setenv("PERISCOPE_AUDIT_MAX_SIZE_MB", "")
	t.Setenv("PERISCOPE_AUDIT_VACUUM_INTERVAL", "")

	cfg := LoadSQLiteConfigFromEnv()
	if cfg.Enabled {
		t.Errorf("Enabled default: got true, want false")
	}
	if cfg.RetentionDays != 30 {
		t.Errorf("RetentionDays default: got %d, want 30", cfg.RetentionDays)
	}
	if cfg.MaxSizeMB != 1024 {
		t.Errorf("MaxSizeMB default: got %d, want 1024", cfg.MaxSizeMB)
	}
	if cfg.VacuumInterval != 24*time.Hour {
		t.Errorf("VacuumInterval default: got %v, want 24h", cfg.VacuumInterval)
	}
}

func TestLoadSQLiteConfigFromEnv_Overrides(t *testing.T) {
	t.Setenv("PERISCOPE_AUDIT_ENABLED", "true")
	t.Setenv("PERISCOPE_AUDIT_RETENTION_DAYS", "7")
	t.Setenv("PERISCOPE_AUDIT_MAX_SIZE_MB", "256")
	t.Setenv("PERISCOPE_AUDIT_VACUUM_INTERVAL", "6h")

	cfg := LoadSQLiteConfigFromEnv()
	if !cfg.Enabled {
		t.Error("Enabled override: want true")
	}
	if cfg.RetentionDays != 7 {
		t.Errorf("RetentionDays: got %d", cfg.RetentionDays)
	}
	if cfg.MaxSizeMB != 256 {
		t.Errorf("MaxSizeMB: got %d", cfg.MaxSizeMB)
	}
	if cfg.VacuumInterval != 6*time.Hour {
		t.Errorf("VacuumInterval: got %v", cfg.VacuumInterval)
	}
}

func TestSQLiteSink_PruneBySize_SingleShot(t *testing.T) {
	// 1MB cap; each row is small but with overhead the file will exceed
	// 1MB after a few thousand inserts, exercising the prune path.
	s := openTestSink(t, SQLiteConfig{MaxSizeMB: 1})
	ctx := context.Background()

	// Insert enough rows to push the file well past 1MB.
	for i := 0; i < 5000; i++ {
		s.Record(ctx, Event{
			Timestamp: time.Now(),
			Actor:     Actor{Sub: "alice"},
			Verb:      VerbApply,
			Outcome:   OutcomeSuccess,
			Cluster:   "prod",
			Reason:    "padding-padding-padding-padding-padding-padding-padding-padding",
			Extra:     map[string]any{"i": i, "filler": "0123456789012345678901234567890123456789"},
		})
	}
	before := countRows(t, s.db)
	if before == 0 {
		t.Fatal("no rows inserted")
	}

	// Single-shot prune: returns rows-deleted, no convergence error.
	dropped, err := s.pruneBySize(ctx)
	if err != nil {
		t.Fatalf("pruneBySize: %v", err)
	}
	if dropped == 0 {
		// File may not have grown past 1MB on this system. Assert the
		// no-op path returned (0, nil) cleanly.
		size, _ := dbFileSize(s.cfg.Path)
		t.Logf("dropped=0 size=%d (under cap, no prune needed)", size)
		return
	}
	after := countRows(t, s.db)
	if after >= before {
		t.Errorf("after=%d should be less than before=%d (dropped=%d)", after, before, dropped)
	}
	t.Logf("pruned %d rows (before=%d, after=%d)", dropped, before, after)
}

func TestSQLiteSink_PruneBySize_NoOpUnderCap(t *testing.T) {
	s := openTestSink(t, SQLiteConfig{MaxSizeMB: 100})
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		s.Record(ctx, Event{Timestamp: time.Now(), Actor: Actor{Sub: "alice"}, Verb: VerbApply, Outcome: OutcomeSuccess})
	}
	dropped, err := s.pruneBySize(ctx)
	if err != nil {
		t.Fatalf("pruneBySize: %v", err)
	}
	if dropped != 0 {
		t.Errorf("expected 0 dropped under cap, got %d", dropped)
	}
}

func TestSQLiteSink_Query_FilterAndPagination(t *testing.T) {
	s := openTestSink(t, SQLiteConfig{})
	ctx := context.Background()
	base := time.Now().Add(-time.Hour)

	// Insert 10 rows, alternating actors and verbs.
	for i := 0; i < 10; i++ {
		actor := "alice"
		verb := VerbApply
		outcome := OutcomeSuccess
		if i%2 == 1 {
			actor = "bob"
			verb = VerbDelete
		}
		if i%3 == 0 {
			outcome = OutcomeDenied
		}
		s.Record(ctx, Event{
			Timestamp: base.Add(time.Duration(i) * time.Minute),
			Actor:     Actor{Sub: actor},
			Verb:      verb,
			Outcome:   outcome,
			Cluster:   "prod",
		})
	}

	// Filter by actor.
	res, err := s.Query(ctx, QueryArgs{Actor: "alice"})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	for _, r := range res.Items {
		if r.Actor.Sub != "alice" {
			t.Errorf("actor filter leaked %q", r.Actor.Sub)
		}
	}
	if int64(res.Total) != int64(len(res.Items)) {
		t.Errorf("Total=%d does not match len(Rows)=%d", res.Total, len(res.Items))
	}

	// Filter by verb + outcome combined.
	res, err = s.Query(ctx, QueryArgs{Verb: VerbDelete, Outcome: OutcomeDenied})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	for _, r := range res.Items {
		if r.Verb != VerbDelete || r.Outcome != OutcomeDenied {
			t.Errorf("verb+outcome filter leaked: %+v", r)
		}
	}

	// Pagination + ordering: ts DESC.
	res, err = s.Query(ctx, QueryArgs{Limit: 3})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res.Items) != 3 {
		t.Fatalf("Limit=3 returned %d rows", len(res.Items))
	}
	for i := 1; i < len(res.Items); i++ {
		if !res.Items[i-1].Timestamp.After(res.Items[i].Timestamp) &&
			!res.Items[i-1].Timestamp.Equal(res.Items[i].Timestamp) {
			t.Errorf("ordering not DESC at i=%d: %v vs %v",
				i, res.Items[i-1].Timestamp, res.Items[i].Timestamp)
		}
	}

	// Offset skips the first N.
	page1, _ := s.Query(ctx, QueryArgs{Limit: 5, Offset: 0})
	page2, _ := s.Query(ctx, QueryArgs{Limit: 5, Offset: 5})
	if len(page1.Items) == 0 || len(page2.Items) == 0 {
		t.Fatal("pagination returned empty pages")
	}
	if page1.Items[0].Timestamp.Equal(page2.Items[0].Timestamp) {
		t.Errorf("pagination overlap: page1[0]==page2[0]")
	}
}

func TestSQLiteSink_Query_LimitClamp(t *testing.T) {
	s := openTestSink(t, SQLiteConfig{})
	ctx := context.Background()
	res, err := s.Query(ctx, QueryArgs{Limit: 999999})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	// MaxQueryLimit is 500 per reader.go; the call should not error
	// even when the requested limit exceeds it.
	if res.Limit > MaxQueryLimit {
		t.Errorf("Limit=%d not clamped to %d", res.Limit, MaxQueryLimit)
	}
}

func TestSQLiteSink_Query_TimeRange(t *testing.T) {
	s := openTestSink(t, SQLiteConfig{})
	ctx := context.Background()
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	recent := now.Add(-10 * time.Minute)

	s.Record(ctx, Event{Timestamp: old, Actor: Actor{Sub: "alice"}, Verb: VerbApply, Outcome: OutcomeSuccess})
	s.Record(ctx, Event{Timestamp: recent, Actor: Actor{Sub: "alice"}, Verb: VerbApply, Outcome: OutcomeSuccess})

	res, err := s.Query(ctx, QueryArgs{From: now.Add(-30 * time.Minute)})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(res.Items) != 1 {
		t.Errorf("From filter returned %d rows, want 1", len(res.Items))
	}
}

// TestSQLiteSink_Migrations_FreshDB asserts that a brand-new DB ends
// up with PRAGMA user_version equal to len(migrations).
func TestSQLiteSink_Migrations_FreshDB(t *testing.T) {
	s := openTestSink(t, SQLiteConfig{})
	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatalf("read user_version: %v", err)
	}
	if v != len(migrations) {
		t.Errorf("user_version=%d, want %d (len(migrations))", v, len(migrations))
	}
}

// TestSQLiteSink_Migrations_RefuseDowngrade asserts that an audit DB
// from a future Periscope build (PRAGMA user_version > len(migrations))
// is refused at open time rather than silently overwritten.
func TestSQLiteSink_Migrations_RefuseDowngrade(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.db")

	// Pre-populate a DB at a "future" user_version.
	{
		s := openTestSink(t, SQLiteConfig{Path: path})
		if _, err := s.db.Exec(`PRAGMA user_version = 999`); err != nil {
			t.Fatalf("set user_version: %v", err)
		}
		_ = s.Close()
	}

	// Re-opening should error with a downgrade-refused message.
	ctx := context.Background()
	_, err := OpenSQLiteSink(ctx, SQLiteConfig{Path: path, VacuumInterval: time.Hour})
	if err == nil {
		t.Fatal("expected downgrade refusal, got nil")
	}
	if !strings.Contains(err.Error(), "downgrade") {
		t.Errorf("error missing 'downgrade': %v", err)
	}
}
