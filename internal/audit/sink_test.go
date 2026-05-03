package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"
)

// captureSink builds a StdoutSink that writes JSON into buf so the
// test can decode and assert on individual fields rather than
// regex-matching a log line.
func captureSink(buf *bytes.Buffer) *StdoutSink {
	logger := slog.New(slog.NewJSONHandler(buf, nil))
	return &StdoutSink{Logger: logger}
}

func decodeOne(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	var got map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
		t.Fatalf("decode: %v\nraw: %s", err, buf.String())
	}
	return got
}

func TestStdoutSink_BaseFields(t *testing.T) {
	var buf bytes.Buffer
	sink := captureSink(&buf)
	sink.Record(context.Background(), Event{
		Timestamp: time.Now().UTC(),
		Verb:      VerbDelete,
		Outcome:   OutcomeSuccess,
		Actor:     Actor{Sub: "user@example"},
		Cluster:   "prod-eu",
		Resource: ResourceRef{
			Group: "apps", Version: "v1", Resource: "deployments",
			Namespace: "shop", Name: "checkout",
		},
	})

	got := decodeOne(t, &buf)
	for k, want := range map[string]string{
		"category":  "audit",
		"event":     "delete",
		"outcome":   "success",
		"actor.sub": "user@example",
		"cluster":   "prod-eu",
		"group":     "apps",
		"version":   "v1",
		"resource":  "deployments",
		"namespace": "shop",
		"name":      "checkout",
	} {
		if got[k] != want {
			t.Errorf("field %q: got %v, want %q", k, got[k], want)
		}
	}
}

func TestStdoutSink_OmitsEmptyOptionalFields(t *testing.T) {
	var buf bytes.Buffer
	sink := captureSink(&buf)
	sink.Record(context.Background(), Event{
		Verb:    VerbExecOpen,
		Outcome: OutcomeSuccess,
		Actor:   Actor{Sub: "u"},
		Cluster: "c",
	})

	got := decodeOne(t, &buf)
	for _, k := range []string{
		"namespace", "name", "group", "resource", "reason",
		"actor.email", "actor.groups", "request_id", "route",
	} {
		if _, present := got[k]; present {
			t.Errorf("optional field %q should be omitted, got %v", k, got[k])
		}
	}
}

func TestStdoutSink_FlattensExtra(t *testing.T) {
	var buf bytes.Buffer
	sink := captureSink(&buf)
	sink.Record(context.Background(), Event{
		Verb:    VerbExecClose,
		Outcome: OutcomeSuccess,
		Actor:   Actor{Sub: "u"},
		Extra: map[string]any{
			"session_id":   "abc",
			"bytes_stdin":  float64(42),
			"bytes_stdout": float64(1024),
			"exit_code":    float64(0),
		},
	})

	got := decodeOne(t, &buf)
	if got["session_id"] != "abc" {
		t.Errorf("session_id: got %v, want abc", got["session_id"])
	}
	if got["bytes_stdin"] != float64(42) {
		t.Errorf("bytes_stdin: got %v, want 42", got["bytes_stdin"])
	}
	if got["exit_code"] != float64(0) {
		t.Errorf("exit_code: got %v, want 0", got["exit_code"])
	}
}

func TestStdoutSink_NilLoggerUsesDefault(t *testing.T) {
	// Just check it doesn't panic. The default logger writes to
	// stderr; we don't care about contents here.
	sink := &StdoutSink{}
	sink.Record(context.Background(), Event{
		Verb: VerbApply, Outcome: OutcomeSuccess,
		Actor: Actor{Sub: "u"},
	})
}

func TestStdoutSink_EmitsRequestContextFields(t *testing.T) {
	var buf bytes.Buffer
	sink := captureSink(&buf)
	sink.Record(context.Background(), Event{
		Verb:      VerbApply,
		Outcome:   OutcomeDenied,
		Actor:     Actor{Sub: "u", Email: "u@x", Groups: []string{"sre"}},
		RequestID: "req-1",
		Route:     "/api/clusters/{cluster}/resources/{group}/{version}/{resource}/{ns}/{name}",
		Reason:    "forbidden: user cannot delete",
	})

	got := decodeOne(t, &buf)
	if got["request_id"] != "req-1" {
		t.Errorf("request_id: got %v", got["request_id"])
	}
	if got["actor.email"] != "u@x" {
		t.Errorf("actor.email: got %v", got["actor.email"])
	}
	if got["reason"] != "forbidden: user cannot delete" {
		t.Errorf("reason: got %v", got["reason"])
	}
	if got["outcome"] != "denied" {
		t.Errorf("outcome: got %v", got["outcome"])
	}
}
