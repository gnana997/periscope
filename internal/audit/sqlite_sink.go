package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// SQLiteConfig is the operator-tunable shape for the persistent
// audit sink. Loaded from PERISCOPE_AUDIT_* environment variables in
// LoadSQLiteConfigFromEnv; the Helm chart renders these from
// values.yaml.
type SQLiteConfig struct {
	// Enabled gates the entire sink. When false, the sink isn't
	// constructed and stdout-only audit emission continues.
	Enabled bool

	// Path is the SQLite database file path. The directory is
	// created if missing. Defaults to /var/lib/periscope/audit/audit.db.
	Path string

	// RetentionDays bounds the row age. Older rows are pruned by
	// the vacuum loop. Set to 0 to disable time-based pruning (size
	// cap still applies).
	RetentionDays int

	// MaxSizeMB is a hard ceiling on the on-disk DB file. When the
	// vacuum loop sees the file exceed this, it prunes oldest rows
	// until under the cap. Set to 0 to disable size-based pruning.
	MaxSizeMB int

	// VacuumInterval is how often the prune+VACUUM loop runs.
	// Defaults to 24h.
	VacuumInterval time.Duration
}

// LoadSQLiteConfigFromEnv reads the audit-sink env vars and applies
// defaults. Compatible with the existing config-loading pattern in
// internal/exec/config.go (env var driven, single-shot at startup).
func LoadSQLiteConfigFromEnv() SQLiteConfig {
	return SQLiteConfig{
		Enabled:        os.Getenv("PERISCOPE_AUDIT_ENABLED") == "true",
		Path:           strDefault(os.Getenv("PERISCOPE_AUDIT_DB_PATH"), "/var/lib/periscope/audit/audit.db"),
		RetentionDays:  intDefault(os.Getenv("PERISCOPE_AUDIT_RETENTION_DAYS"), 30),
		MaxSizeMB:      intDefault(os.Getenv("PERISCOPE_AUDIT_MAX_SIZE_MB"), 1024),
		VacuumInterval: durationDefault(os.Getenv("PERISCOPE_AUDIT_VACUUM_INTERVAL"), 24*time.Hour),
	}
}

func strDefault(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func intDefault(v string, fallback int) int {
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return fallback
	}
	return n
}

func durationDefault(v string, fallback time.Duration) time.Duration {
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}

// SQLiteSink writes audit events to a local SQLite database.
//
// Design notes:
//
//   - WAL journal mode + 5s busy_timeout so concurrent reads (a future
//     /api/audit endpoint) don't block writes.
//   - Errors during Record are logged and swallowed — the sink must
//     not block a privileged request. Operators see drops via
//     stderr; the stdout sink is always attached as a fallback.
//   - The retention loop runs in a single goroutine. It prunes by
//     age first, then re-checks size, then VACUUMs if either pruned
//     anything. VACUUM is expensive on large DBs; we only run it
//     when there's something to reclaim.
//   - Schema is one wide table with normalized columns for the
//     common query dimensions (actor, cluster, verb, ts, outcome) and
//     a JSON `extra` column for verb-specific fields. Indexes match
//     the queries we expect.
type SQLiteSink struct {
	cfg SQLiteConfig
	db  *sql.DB

	insertMu   sync.Mutex
	insertStmt *sql.Stmt
}

const sqliteSchema = `
CREATE TABLE IF NOT EXISTS audit_events (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    ts_unix_nano  INTEGER NOT NULL,
    request_id    TEXT,
    route         TEXT,
    actor_sub     TEXT NOT NULL,
    actor_email   TEXT,
    actor_groups  TEXT,
    verb          TEXT NOT NULL,
    outcome       TEXT NOT NULL,
    cluster       TEXT,
    res_group     TEXT,
    res_version   TEXT,
    res_type      TEXT,
    res_namespace TEXT,
    res_name      TEXT,
    reason        TEXT,
    extra         TEXT
);

CREATE INDEX IF NOT EXISTS idx_audit_ts        ON audit_events (ts_unix_nano);
CREATE INDEX IF NOT EXISTS idx_audit_actor_ts  ON audit_events (actor_sub, ts_unix_nano);
CREATE INDEX IF NOT EXISTS idx_audit_verb_ts   ON audit_events (verb, ts_unix_nano);
CREATE INDEX IF NOT EXISTS idx_audit_outcome_ts ON audit_events (outcome, ts_unix_nano);
CREATE INDEX IF NOT EXISTS idx_audit_scope_ts  ON audit_events (cluster, res_namespace, ts_unix_nano);
`

const insertSQL = `
INSERT INTO audit_events (
    ts_unix_nano, request_id, route,
    actor_sub, actor_email, actor_groups,
    verb, outcome, cluster,
    res_group, res_version, res_type, res_namespace, res_name,
    reason, extra
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`

// OpenSQLiteSink constructs the sink, ensures the DB and its parent
// directory exist, runs the idempotent schema migration, and starts
// the retention/vacuum goroutine. Returns an error if the DB cannot
// be opened — main() decides whether to log and continue (per the
// fail-open policy) or panic.
func OpenSQLiteSink(ctx context.Context, cfg SQLiteConfig) (*SQLiteSink, error) {
	if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
		return nil, fmt.Errorf("audit: mkdir %s: %w", filepath.Dir(cfg.Path), err)
	}
	dsn := cfg.Path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("audit: open sqlite: %w", err)
	}
	// modernc.org/sqlite is a CGO-free driver; pings cheaply
	// validate the connection / pragmas.
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("audit: ping sqlite: %w", err)
	}
	if _, err := db.ExecContext(ctx, sqliteSchema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("audit: migrate schema: %w", err)
	}
	stmt, err := db.PrepareContext(ctx, insertSQL)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("audit: prepare insert: %w", err)
	}
	s := &SQLiteSink{cfg: cfg, db: db, insertStmt: stmt}
	go s.retentionLoop(ctx)
	return s, nil
}

// Record inserts one row. Errors are logged and swallowed so a
// transiently-failing audit DB can never block a privileged action.
// The matching stdout sink, attached unconditionally, ensures
// nothing is lost from the operator-visible log stream.
func (s *SQLiteSink) Record(ctx context.Context, evt Event) {
	groupsJSON, _ := json.Marshal(evt.Actor.Groups)
	extraJSON, _ := json.Marshal(evt.Extra)

	s.insertMu.Lock()
	_, err := s.insertStmt.ExecContext(ctx,
		evt.Timestamp.UnixNano(),
		nullable(evt.RequestID),
		nullable(evt.Route),
		evt.Actor.Sub,
		nullable(evt.Actor.Email),
		nullableBytes(groupsJSON, len(evt.Actor.Groups) == 0),
		string(evt.Verb),
		string(evt.Outcome),
		nullable(evt.Cluster),
		nullable(evt.Resource.Group),
		nullable(evt.Resource.Version),
		nullable(evt.Resource.Resource),
		nullable(evt.Resource.Namespace),
		nullable(evt.Resource.Name),
		nullable(evt.Reason),
		nullableBytes(extraJSON, len(evt.Extra) == 0),
	)
	s.insertMu.Unlock()

	if err != nil {
		slog.ErrorContext(ctx, "audit: sqlite insert failed",
			"err", err, "verb", evt.Verb, "actor", evt.Actor.Sub)
	}
}

// Close stops accepting new writes and closes the underlying DB.
// Safe to call when ctx-cancellation has already terminated the
// retention loop.
func (s *SQLiteSink) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	if s.insertStmt != nil {
		_ = s.insertStmt.Close()
	}
	return s.db.Close()
}

// retentionLoop runs prune-by-age and prune-by-size at
// VacuumInterval until ctx is canceled. A first pass kicks off
// immediately so a pod that just woke up with stale data starts
// reclaiming space without waiting a full interval.
func (s *SQLiteSink) retentionLoop(ctx context.Context) {
	if s.cfg.RetentionDays == 0 && s.cfg.MaxSizeMB == 0 {
		return
	}
	tick := time.NewTicker(s.cfg.VacuumInterval)
	defer tick.Stop()

	s.runRetention(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.runRetention(ctx)
		}
	}
}

func (s *SQLiteSink) runRetention(ctx context.Context) {
	pruned := false
	if s.cfg.RetentionDays > 0 {
		cutoff := time.Now().Add(-time.Duration(s.cfg.RetentionDays) * 24 * time.Hour).UnixNano()
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM audit_events WHERE ts_unix_nano < ?`, cutoff)
		if err != nil {
			slog.ErrorContext(ctx, "audit: prune by age failed", "err", err)
		} else if n, _ := res.RowsAffected(); n > 0 {
			pruned = true
			slog.InfoContext(ctx, "audit: pruned by age",
				"rows", n, "retention_days", s.cfg.RetentionDays)
		}
	}
	if s.cfg.MaxSizeMB > 0 {
		if n, err := s.pruneBySize(ctx); err != nil {
			slog.ErrorContext(ctx, "audit: prune by size failed", "err", err)
		} else if n > 0 {
			pruned = true
			slog.InfoContext(ctx, "audit: pruned by size",
				"rows", n, "max_size_mb", s.cfg.MaxSizeMB)
		}
	}
	if pruned {
		// VACUUM reclaims the freed pages back to the OS. Without
		// it the file stays as large as the high-water mark.
		if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
			slog.ErrorContext(ctx, "audit: vacuum failed", "err", err)
		}
	}
}

// pruneBySize repeatedly deletes the oldest 10% of rows until the
// on-disk file is under MaxSizeMB. Capped at 10 iterations so a
// pathologically wrong cap doesn't lock the loop.
func (s *SQLiteSink) pruneBySize(ctx context.Context) (int64, error) {
	target := int64(s.cfg.MaxSizeMB) * 1024 * 1024
	var total int64
	for i := 0; i < 10; i++ {
		size, err := dbFileSize(s.cfg.Path)
		if err != nil {
			return total, err
		}
		if size <= target {
			return total, nil
		}
		var rowCount int64
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM audit_events`).Scan(&rowCount); err != nil {
			return total, err
		}
		if rowCount == 0 {
			return total, nil
		}
		// Drop the oldest 10% in one shot. Bounded so a small DB
		// converges in a few iterations and a giant DB doesn't
		// take a single multi-second DELETE.
		batch := rowCount / 10
		if batch < 1 {
			batch = 1
		}
		res, err := s.db.ExecContext(ctx,
			`DELETE FROM audit_events
			 WHERE id IN (
			     SELECT id FROM audit_events ORDER BY ts_unix_nano ASC LIMIT ?
			 )`, batch)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
		if n == 0 {
			return total, nil
		}
		// VACUUM between batches so dbFileSize reflects the prune.
		if _, err := s.db.ExecContext(ctx, `VACUUM`); err != nil {
			return total, err
		}
	}
	return total, errors.New("size-prune did not converge in 10 iterations")
}

func dbFileSize(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

// nullable returns a *string that's nil when v is empty so
// SQLite stores NULL rather than empty strings — keeps queries
// like `WHERE namespace IS NULL` honest.
func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullableBytes(b []byte, empty bool) any {
	if empty {
		return nil
	}
	return string(b)
}
