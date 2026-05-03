package audit

import (
	"context"
	"testing"
)

func TestRequestContextFrom_ZeroWhenAbsent(t *testing.T) {
	got := RequestContextFrom(context.Background())
	if got.RequestID != "" || got.Route != "" || got.Actor.Sub != "" {
		t.Errorf("expected zero RequestContext, got %+v", got)
	}
}

func TestWithRequestContextRoundTrip(t *testing.T) {
	rc := RequestContext{RequestID: "r1", Route: "/x", Actor: Actor{Sub: "u"}}
	ctx, _ := WithRequestContext(context.Background(), rc)

	got := RequestContextFrom(ctx)
	if got.RequestID != rc.RequestID || got.Route != rc.Route || got.Actor.Sub != rc.Actor.Sub {
		t.Errorf("roundtrip mismatch: got %+v want %+v", got, rc)
	}
}

func TestPatchActor_UpdatesInPlace(t *testing.T) {
	ctx, _ := WithRequestContext(context.Background(), RequestContext{
		RequestID: "r1",
	})

	PatchActor(ctx, Actor{Sub: "u@x", Email: "u@x"})

	got := RequestContextFrom(ctx)
	if got.RequestID != "r1" {
		t.Errorf("RequestID lost: %q", got.RequestID)
	}
	if got.Actor.Sub != "u@x" || got.Actor.Email != "u@x" {
		t.Errorf("Actor not patched: %+v", got.Actor)
	}
}

func TestPatchActor_NoopWhenNoContext(t *testing.T) {
	// Must not panic when RequestContext was never planted.
	PatchActor(context.Background(), Actor{Sub: "u"})
}
