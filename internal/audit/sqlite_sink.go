package audit

import (
	"context"
	"database/sql"
	"encoding/json"
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

// Validate inspects the loaded config for footguns and returns a
// (possibly empty) slice of human-readable warnings. main() logs
// each warning at slog.Warn level rather than failing — a
// misconfigured audit cap shouldn't block the pod from booting, but
// the operator deserves to know.
//
// Validate is best-effort: it stat's the parent directory to read
// available disk space, which only works after the directory is
// mounted (post-Helm-render, in-pod). Calling it before the volume
// is ready returns the warnings derivable from the config alone.
func (c SQLiteConfig) Validate() []string {
	var warns []string

	// Both caps disabled = unbounded growth until the volume itself
	// fills. Catch the combo even though either cap alone is fine.
	if c.RetentionDays == 0 && c.MaxSizeMB == 0 {
		warns = append(warns,
			"audit: both retentionDays and maxSizeMB are 0 — "+
				"the DB will grow until the volume fills")
	}
	if c.VacuumInterval > 0 && c.VacuumInterval < time.Minute {
		warns = append(warns,
			"audit: vacuumInterval < 1m — pruning will hammer disk; "+
				"consider 6h or 24h")
	}
	if c.RetentionDays > 365 {
		warns = append(warns,
			"audit: retentionDays > 365 — SQLite is a local cache, "+
				"not compliance storage; forward stdout to an external SIEM")
	}

	// Disk-free vs cap. If the parent dir doesn't exist yet, skip
	// silently — main() may call Validate before OpenSQLiteSink.
	if c.MaxSizeMB > 0 {
		if avail, err := availableDiskMB(filepath.Dir(c.Path)); err == nil {
			// VACUUM can transiently double the file. Plus WAL.
			// Warn if the cap exceeds 50% of available disk.
			needed := int64(c.MaxSizeMB) * 2
			if needed > avail {
				warns = append(warns, fmt.Sprintf(
					"audit: maxSizeMB=%d exceeds 50%% of available disk (%dMi) "+
						"at %s — VACUUM may fail or kubelet may evict the pod",
					c.MaxSizeMB, avail, filepath.Dir(c.Path)))
			}
		}
	}
	return warns
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

const schemaV1 = `
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

// migrations is the ordered list of schema versions. The slice index
// + 1 is the version number stored in PRAGMA user_version. Append a
// new entry when the schema needs to evolve — never edit existing
// entries (they may have already run on production DBs).
var migrations = []string{
	schemaV1,
}

// runMigrations applies any unapplied schema migrations to the open
// DB inside a single transaction per migration, then bumps
// PRAGMA user_version. Idempotent on a fully-migrated DB.
//
// Failure mid-migration aborts the transaction; callers (OpenSQLiteSink)
// surface the error so main() can fail-open to stdout-only audit.
func runMigrations(ctx context.Context, db *sql.DB) error {
	var current int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	target := len(migrations)
	if current > target {
		// DB is from a newer Periscope build than this binary. Refuse
		// to downgrade; operator must roll forward.
		return fmt.Errorf("audit DB is at schema v%d, this binary only knows v%d (downgrade not supported)", current, target)
	}
	for v := current; v < target; v++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration v%d: %w", v+1, err)
		}
		if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration v%d: %w", v+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", v+1)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("set user_version v%d: %w", v+1, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration v%d: %w", v+1, err)
		}
	}
	return nil
}

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
	// wal_autocheckpoint=1000 (the default in pages, ~4 MiB) is set
	// explicitly so the value is visible at the call site rather
	// than relying on SQLite's compiled-in default. The retention
	// loop also runs PRAGMA wal_checkpoint(TRUNCATE) after each
	// prune so the WAL file is bounded — without that, a sustained
	// burst of writes can grow audit.db-wal alongside audit.db and
	// blow past the on-disk budget the operator configured.
	dsn := cfg.Path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=wal_autocheckpoint(1000)"
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
	if err := runMigrations(ctx, db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("audit: migrate schema: %w", err)
	}
	stmt, err := db.PrepareContext(ctx, insertSQL)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("audit: prepare insert: %w", err)
	}
	s := &SQLiteSink{cfg: cfg, db: db, insertStmt: stmt}
	// Run an initial retention pass synchronously before returning
	// so a pod that's just woken up with stale data (e.g. after a
	// long downtime) starts reclaiming space without waiting a full
	// VacuumInterval. Also avoids a goroutine-scheduling race with
	// callers that immediately Record() events on the new sink.
	if cfg.RetentionDays > 0 || cfg.MaxSizeMB > 0 {
		// Bound the initial sweep so a stale DB on a slow PVC cannot fail
		// the readiness probe. The regular vacuum loop catches up on its
		// 24h cadence if this round hits the deadline.
		initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		s.runRetention(initCtx)
		cancel()
	}
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
// VacuumInterval until ctx is canceled. The initial pass is run
// synchronously by OpenSQLiteSink before this goroutine starts.
func (s *SQLiteSink) retentionLoop(ctx context.Context) {
	if s.cfg.RetentionDays == 0 && s.cfg.MaxSizeMB == 0 {
		return
	}
	tick := time.NewTicker(s.cfg.VacuumInterval)
	defer tick.Stop()

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
		// Truncate the WAL file. Without this, audit.db-wal can
		// keep growing alongside audit.db on busy writers, eating
		// into the operator's on-disk budget. wal_checkpoint(TRUNCATE)
		// blocks until all pages are checkpointed and then resets
		// the WAL file size to zero.
		if _, err := s.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			slog.ErrorContext(ctx, "audit: wal checkpoint failed", "err", err)
		}
	}
}

// pruneBySize repeatedly deletes the oldest 10% of rows until the
// on-disk file is under MaxSizeMB. Capped at 10 iterations so a
// pathologically wrong cap doesn't lock the loop.
func (s *SQLiteSink) pruneBySize(ctx context.Context) (int64, error) {
	// Single-shot prune. Estimate the fraction of rows to drop from the
	// (currentSize, targetSize) ratio, delete that many oldest rows in
	// one DELETE, return. The post-prune VACUUM in runRetention reclaims
	// pages back to the OS in a single pass.
	//
	// Why single-shot rather than a loop: without an in-loop VACUUM the
	// file size doesn't shrink between iterations, so any size-based
	// convergence test would never converge. Estimating up front is the
	// same total work and avoids a misleading "did not converge" error
	// when the loop simply lacked a way to observe progress.
	target := int64(s.cfg.MaxSizeMB) * 1024 * 1024
	size, err := dbFileSize(s.cfg.Path)
	if err != nil {
		return 0, err
	}
	if size <= target {
		return 0, nil
	}
	var rowCount int64
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit_events`).Scan(&rowCount); err != nil {
		return 0, err
	}
	if rowCount == 0 {
		return 0, nil
	}
	// 10% headroom: drop slightly more than the ratio suggests so we land
	// comfortably under the cap after VACUUM rather than just at it.
	excess := float64(size-target) / float64(size)
	toDrop := int64(float64(rowCount) * excess * 1.1)
	if toDrop < 1 {
		toDrop = 1
	}
	if toDrop > rowCount {
		toDrop = rowCount
	}
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM audit_events
		 WHERE id IN (
		     SELECT id FROM audit_events ORDER BY ts_unix_nano ASC LIMIT ?
		 )`, toDrop)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

func dbFileSize(path string) (int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}


// Query implements Reader. Builds the SELECT dynamically from
// QueryArgs — every filter is parameterized; nothing is interpolated.
//
// Result ordering is (ts_unix_nano DESC, id DESC) so two rows that
// share a timestamp paginate deterministically. The total count is
// computed in a separate query against the same WHERE clause; for
// our retention-capped DB that's well within milliseconds.
func (s *SQLiteSink) Query(ctx context.Context, q QueryArgs) (QueryResult, error) {
	where, args := buildQueryWhere(q)

	limit := q.Limit
	if limit <= 0 {
		limit = DefaultQueryLimit
	}
	if limit > MaxQueryLimit {
		limit = MaxQueryLimit
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}

	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM audit_events `+where, args...).Scan(&total); err != nil {
		return QueryResult{}, fmt.Errorf("audit: count: %w", err)
	}

	listSQL := `
		SELECT id, ts_unix_nano, request_id,
		       actor_sub, actor_email, actor_groups,
		       verb, outcome, cluster,
		       res_group, res_version, res_type, res_namespace, res_name,
		       reason, extra
		FROM audit_events ` + where + `
		ORDER BY ts_unix_nano DESC, id DESC
		LIMIT ? OFFSET ?`

	listArgs := append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, listSQL, listArgs...)
	if err != nil {
		return QueryResult{}, fmt.Errorf("audit: select: %w", err)
	}
	defer rows.Close()

	items := make([]Row, 0, limit)
	for rows.Next() {
		r, err := scanRow(rows)
		if err != nil {
			return QueryResult{}, fmt.Errorf("audit: scan: %w", err)
		}
		items = append(items, r)
	}
	if err := rows.Err(); err != nil {
		return QueryResult{}, fmt.Errorf("audit: rows: %w", err)
	}

	return QueryResult{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	}, nil
}

// buildQueryWhere assembles the parameterized WHERE clause. Returns
// the clause (including the leading "WHERE " when non-empty, "" when
// no filters) and the matching args slice in placeholder order.
func buildQueryWhere(q QueryArgs) (string, []any) {
	var clauses []string
	var args []any

	if !q.From.IsZero() {
		clauses = append(clauses, "ts_unix_nano >= ?")
		args = append(args, q.From.UnixNano())
	}
	if !q.To.IsZero() {
		clauses = append(clauses, "ts_unix_nano < ?")
		args = append(args, q.To.UnixNano())
	}
	if q.Actor != "" {
		clauses = append(clauses, "actor_sub = ?")
		args = append(args, q.Actor)
	}
	if q.Verb != "" {
		clauses = append(clauses, "verb = ?")
		args = append(args, string(q.Verb))
	}
	if q.Outcome != "" {
		clauses = append(clauses, "outcome = ?")
		args = append(args, string(q.Outcome))
	}
	if q.Cluster != "" {
		clauses = append(clauses, "cluster = ?")
		args = append(args, q.Cluster)
	}
	if q.Namespace != "" {
		clauses = append(clauses, "res_namespace = ?")
		args = append(args, q.Namespace)
	}
	if q.ResourceName != "" {
		clauses = append(clauses, "res_name = ?")
		args = append(args, q.ResourceName)
	}
	if q.RequestID != "" {
		clauses = append(clauses, "request_id = ?")
		args = append(args, q.RequestID)
	}

	if len(clauses) == 0 {
		return "", nil
	}
	return "WHERE " + joinAnd(clauses), args
}

func joinAnd(parts []string) string {
	out := parts[0]
	for _, p := range parts[1:] {
		out += " AND " + p
	}
	return out
}

// scanRow materializes one DB row into the wire-shape Row struct.
// Nullable columns come back as *sql.NullString so we can distinguish
// "field absent" (NULL) from "empty string" — the StdoutSink omits
// empty fields too, so the JSON we emit is symmetric.
func scanRow(rows interface {
	Scan(dest ...any) error
}) (Row, error) {
	var (
		r            Row
		tsNano       int64
		reqID, email sql.NullString
		groupsJSON   sql.NullString
		cluster      sql.NullString
		grp, ver     sql.NullString
		rtype, ns    sql.NullString
		name, reason sql.NullString
		extraJSON    sql.NullString
	)
	if err := rows.Scan(
		&r.ID,
		&tsNano,
		&reqID,
		&r.Actor.Sub,
		&email,
		&groupsJSON,
		&r.Verb,
		&r.Outcome,
		&cluster,
		&grp,
		&ver,
		&rtype,
		&ns,
		&name,
		&reason,
		&extraJSON,
	); err != nil {
		return Row{}, err
	}
	r.Timestamp = time.Unix(0, tsNano).UTC()
	r.RequestID = nullStr(reqID)
	r.Actor.Email = nullStr(email)
	if groupsJSON.Valid && groupsJSON.String != "" {
		_ = json.Unmarshal([]byte(groupsJSON.String), &r.Actor.Groups)
	}
	r.Cluster = nullStr(cluster)
	r.Resource = ResourceRef{
		Group:     nullStr(grp),
		Version:   nullStr(ver),
		Resource:  nullStr(rtype),
		Namespace: nullStr(ns),
		Name:      nullStr(name),
	}
	r.Reason = nullStr(reason)
	if extraJSON.Valid && extraJSON.String != "" {
		_ = json.Unmarshal([]byte(extraJSON.String), &r.Extra)
	}
	return r, nil
}

func nullStr(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
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
