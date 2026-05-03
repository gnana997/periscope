// Package audit defines the event shape and emission pipeline for
// Periscope's audit trail.
//
// Every privileged action — pod exec, secret reveal, resource
// apply/delete, cronjob trigger — is recorded as a single
// audit.Event flowed through an Emitter. The Emitter fans out to
// one or more Sinks (today: stdout JSON; future: SQLite, external).
//
// Why a dedicated package: prior to this refactor the same logical
// event was emitted as ad-hoc slog calls at each handler with three
// different field shapes, with no audit row at all on the failure
// path. Pinning a single shape here lets every handler emit the same
// way, lets a downstream sink rely on stable field names, and gives
// us one place to add future cross-cutting concerns (signing,
// shipping to a SIEM, redaction).
//
// Stdlib-only by design — the audit pipeline must not pull in heavy
// dependencies because every privileged code path runs through it.
package audit

import "time"

// Verb classifies what the actor did. The set is closed: a new verb
// should be added explicitly here rather than passed as a free string,
// so downstream queries (and the future SQLite schema) can index on
// it.
type Verb string

const (
	VerbCreate       Verb = "create"
	VerbUpdate       Verb = "update"
	VerbPatch        Verb = "patch"
	VerbDelete       Verb = "delete"
	VerbTrigger      Verb = "trigger"
	VerbExecOpen     Verb = "exec_open"
	VerbExecClose    Verb = "exec_close"
	VerbSecretReveal Verb = "secret_reveal"
	// VerbLogOpen is reserved for pod/workload log stream opens. No
	// emission site exists yet; declared so the taxonomy is visible
	// and a follow-up PR can wire it without revisiting this file.
	VerbLogOpen Verb = "log_open"
)

// Outcome is the result classification.
//
//   - Success: the action completed.
//   - Failure: the action errored for a non-authorization reason
//     (validation, conflict, server error, network).
//   - Denied: the action was rejected by Kubernetes RBAC (Forbidden
//     or Unauthorized). Denials are forensically the most
//     interesting class — surfacing them as a distinct outcome
//     means an operator can answer "who tried X and got blocked"
//     with a single query.
type Outcome string

const (
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
	OutcomeDenied  Outcome = "denied"
)

// Actor is the identity slice copied from credentials.Session at
// emission time. We snapshot rather than holding a pointer so a later
// sink can serialize the event without re-reading request context.
type Actor struct {
	Sub    string
	Email  string
	Groups []string
}

// ResourceRef is the Kubernetes object the action targeted.
//
// All fields are optional — an exec event leaves Group/Version/Resource
// empty (the target is implicit in the verb), a cluster-scoped resource
// leaves Namespace empty, and an apply against a yet-to-be-named object
// leaves Name empty. Empty strings are written as empty strings; sinks
// don't need to special-case absence.
type ResourceRef struct {
	Group     string
	Version   string
	Resource  string
	Namespace string
	Name      string
}

// Event is the single shape every audit emission produces.
//
// Top-level fields are the ones every event carries. Verb-specific
// fields (exec byte counts, secret key name, jobName, dryRun) ride
// in Extra so adding a new field for one verb doesn't churn the
// struct or every sink.
type Event struct {
	// Timestamp is set by the Emitter if zero — handlers should not
	// need to remember to fill this.
	Timestamp time.Time

	// RequestID is chi's per-request ID, populated by httpx.RequestID.
	// Lets a single audit row tie back to access logs and
	// reverse-proxy logs.
	RequestID string

	// Route is the chi route pattern (e.g. /api/clusters/{cluster}/...).
	// Useful for grouping audit rows by endpoint without parsing the
	// path.
	Route string

	Actor    Actor
	Verb     Verb
	Outcome  Outcome
	Cluster  string
	Resource ResourceRef

	// Reason is the human-readable explanation. For OutcomeFailure /
	// OutcomeDenied this is err.Error(); for OutcomeSuccess on an
	// exec_close it is the close reason ("completed" / "idle_timeout"
	// / "abort"). Empty for plain successes.
	Reason string

	// Extra carries verb-specific fields. Sinks flatten this into
	// their output (StdoutSink lifts each entry to a top-level slog
	// key). Keep keys lowercase_snake_case for consistency with the
	// existing log shape.
	Extra map[string]any
}
