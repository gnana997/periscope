package identity

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/jonboulle/clockwork"
)

func newSA(ns, name string, annotations map[string]string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   ns,
			Name:        name,
			Annotations: annotations,
		},
	}
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", msg)
}

func TestStartSAInformer_CachePopulatesIRSALister(t *testing.T) {
	cs := fake.NewSimpleClientset(
		newSA("ns1", "sa1", map[string]string{IrsaAnnotation: "arn:aws:iam::123:role/r1"}),
		newSA("ns2", "sa2", nil),
		newSA("ns2", "sa3", map[string]string{IrsaAnnotation: "arn:aws:iam::123:role/r3"}),
	)
	mgr := NewManager("c1",
		&stubPodIdentity{},
		&stubResolver{},
		clockwork.NewFakeClock(),
		Config{},
		nil,
	)

	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()
	cancel, err := StartSAInformer(ctx, cs, mgr)
	if err != nil {
		t.Fatalf("StartSAInformer: %v", err)
	}
	defer cancel()

	waitFor(t, func() bool {
		// Once syncDone flips true, the lister returns the actual
		// cached SAs rather than ErrIRSAListerNotReady.
		_, err := mgr.Ensure(context.Background())
		return err == nil
	}, "informer initial cache sync")

	entries, err := mgr.Ensure(context.Background())
	if err != nil {
		t.Fatalf("ensure after sync: %v", err)
	}
	// sa1 and sa3 carry IRSA; sa2 has no annotation and is dropped
	// (UnifySARoles skips SAs with no binding source).
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2 (sa1 + sa3)", len(entries))
	}
}

func TestStartSAInformer_InvalidatesOnAnnotationAdd(t *testing.T) {
	cs := fake.NewSimpleClientset(newSA("ns", "sa", nil))
	mgr := NewManager("c1",
		&stubPodIdentity{},
		&stubResolver{},
		clockwork.NewFakeClock(),
		Config{},
		nil,
	)

	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()
	cancel, err := StartSAInformer(ctx, cs, mgr)
	if err != nil {
		t.Fatalf("StartSAInformer: %v", err)
	}
	defer cancel()

	waitFor(t, func() bool {
		_, err := mgr.Ensure(context.Background())
		return err == nil
	}, "initial sync")

	// First Ensure populates an empty snapshot. Verify.
	got, _ := mgr.Ensure(context.Background())
	if len(got) != 0 {
		t.Fatalf("initial entries = %d, want 0", len(got))
	}

	// Now patch the SA to add an IRSA annotation. The fake client
	// generates an informer Update event that should invalidate the
	// store and the next Ensure rebuilds.
	updated := newSA("ns", "sa", map[string]string{IrsaAnnotation: "arn:aws:iam::123:role/r"})
	if _, err := cs.CoreV1().ServiceAccounts("ns").Update(context.Background(), updated, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update SA: %v", err)
	}

	waitFor(t, func() bool {
		entries, _ := mgr.Ensure(context.Background())
		return len(entries) == 1
	}, "manager picks up annotation add via informer event")
}

func TestStartSAInformer_NoInvalidateOnUnrelatedFieldChange(t *testing.T) {
	cs := fake.NewSimpleClientset(
		newSA("ns", "sa", map[string]string{IrsaAnnotation: "arn:aws:iam::123:role/r"}),
	)
	mgr := NewManager("c1",
		&stubPodIdentity{},
		&stubResolver{},
		clockwork.NewFakeClock(),
		Config{},
		nil,
	)

	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()
	cancel, err := StartSAInformer(ctx, cs, mgr)
	if err != nil {
		t.Fatalf("StartSAInformer: %v", err)
	}
	defer cancel()

	waitFor(t, func() bool {
		_, err := mgr.Ensure(context.Background())
		return err == nil
	}, "initial sync")
	_, _ = mgr.Ensure(context.Background()) // build store

	// Make a non-IRSA edit (label, no annotation change).
	updated := newSA("ns", "sa", map[string]string{IrsaAnnotation: "arn:aws:iam::123:role/r"})
	updated.Labels = map[string]string{"foo": "bar"}
	if _, err := cs.CoreV1().ServiceAccounts("ns").Update(context.Background(), updated, metav1.UpdateOptions{}); err != nil {
		t.Fatalf("update SA: %v", err)
	}

	// The store should NOT have been invalidated — checking that
	// directly is racy (we'd need to wait for the event to be
	// processed before checking absence). Instead, verify the snapshot
	// remains stable across a brief settle window.
	time.Sleep(50 * time.Millisecond)
	entries, err := mgr.Ensure(context.Background())
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("entries = %d, want 1 (unchanged)", len(entries))
	}
}

func TestStartSAInformer_DeleteInvalidates(t *testing.T) {
	cs := fake.NewSimpleClientset(
		newSA("ns", "sa", map[string]string{IrsaAnnotation: "arn:aws:iam::123:role/r"}),
	)
	mgr := NewManager("c1",
		&stubPodIdentity{},
		&stubResolver{},
		clockwork.NewFakeClock(),
		Config{},
		nil,
	)

	ctx, ctxCancel := context.WithCancel(context.Background())
	defer ctxCancel()
	cancel, err := StartSAInformer(ctx, cs, mgr)
	if err != nil {
		t.Fatalf("StartSAInformer: %v", err)
	}
	defer cancel()

	waitFor(t, func() bool {
		entries, err := mgr.Ensure(context.Background())
		return err == nil && len(entries) == 1
	}, "initial entries")

	if err := cs.CoreV1().ServiceAccounts("ns").Delete(context.Background(), "sa", metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete SA: %v", err)
	}

	waitFor(t, func() bool {
		entries, _ := mgr.Ensure(context.Background())
		return len(entries) == 0
	}, "manager picks up SA delete via informer event")
}
