package main

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/gnana997/periscope/internal/audit"
	"github.com/gnana997/periscope/internal/awseks/identity"
	"github.com/gnana997/periscope/internal/clusters"
	"github.com/gnana997/periscope/internal/credentials"
	"github.com/gnana997/periscope/internal/k8s"
)

// identityFanoutLimit caps concurrent DescribeAccessEntry /
// ListAssociatedAccessPolicies fan-outs. Realistic clusters have ≤30
// access entries; 8 keeps a misbehaving region from holding the
// goroutine pool open without bursting EKS API rate limits.
const identityFanoutLimit = 8

// identityListMaxResults is the per-page cap for ListAccessEntries /
// ListPodIdentityAssociations. EKS service quotas allow up to 100.
const identityListMaxResults = 100

// ── Per-AWS-call audit emission ──────────────────────────────────

// emitIdentityRead writes one audit row per AWS API call. The
// granularity is intentionally fine — one Describe / List call =
// one row — so a forensic reviewer can attribute every SDK call to
// the requesting actor. Operators who find this too chatty can
// filter on `op` in the audit feed.
func emitIdentityRead(ctx context.Context, emitter *audit.Emitter, c clusters.Cluster, outcome audit.Outcome, op, reason string) {
	if emitter == nil {
		return
	}
	emitter.Record(ctx, audit.Event{
		Actor:   actorFromContext(ctx),
		Verb:    audit.VerbAwsIdentityRead,
		Outcome: outcome,
		Cluster: c.Name,
		Reason:  reason,
		Extra: map[string]any{
			"op": op,
		},
	})
}

// ── /identity/access-entries ────────────────────────────────────

func identityAccessEntriesHandler(reg *clusters.Registry, awsCfg aws.Config, emitter *audit.Emitter) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		if !c.EKSCapable() {
			writeAPIErrorJSON(w, http.StatusUnprocessableEntity, errBackendNotEKSCode,
				"identity is only available for EKS-backed clusters")
			return
		}

		client := newIdentityClient(awsCfg, c)
		eksName := c.EKSName()

		principalARNs, err := client.ListAccessEntries(r.Context(), eksName)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			emitIdentityRead(r.Context(), emitter, c, audit.OutcomeFailure, "list_access_entries", err.Error())
			status, code := awsErrorToStatus(err)
			writeAPIErrorJSON(w, status, code, "failed to list access entries: "+err.Error())
			return
		}
		emitIdentityRead(r.Context(), emitter, c, audit.OutcomeSuccess, "list_access_entries", "")

		entries := make([]identity.AccessEntry, len(principalARNs))
		errCh := make(chan error, len(principalARNs))
		sem := make(chan struct{}, identityFanoutLimit)
		var wg sync.WaitGroup

		for i, arn := range principalARNs {
			wg.Add(1)
			sem <- struct{}{}
			go func(i int, arn string) {
				defer wg.Done()
				defer func() { <-sem }()
				ent, derr := client.DescribeAccessEntry(r.Context(), eksName, arn)
				if derr != nil {
					emitIdentityRead(r.Context(), emitter, c, audit.OutcomeFailure, "describe_access_entry", derr.Error())
					errCh <- derr
					return
				}
				emitIdentityRead(r.Context(), emitter, c, audit.OutcomeSuccess, "describe_access_entry", "")
				policies, perr := client.ListAssociatedAccessPolicies(r.Context(), eksName, arn)
				if perr != nil {
					emitIdentityRead(r.Context(), emitter, c, audit.OutcomeFailure, "list_associated_policies", perr.Error())
					// Soft-fail: render the entry without policies rather
					// than dropping it entirely.
					ent.AccessPolicies = nil
				} else {
					emitIdentityRead(r.Context(), emitter, c, audit.OutcomeSuccess, "list_associated_policies", "")
					ent.AccessPolicies = policies
				}
				entries[i] = ent
			}(i, arn)
		}
		wg.Wait()
		close(errCh)

		if firstErr := <-errCh; firstErr != nil {
			if errors.Is(firstErr, context.Canceled) {
				return
			}
			status, code := awsErrorToStatus(firstErr)
			writeAPIErrorJSON(w, status, code, "failed to describe access entries: "+firstErr.Error())
			return
		}

		writeJSON(w, http.StatusOK, entries)
	}
}

// ── /identity/aws-auth-diff ─────────────────────────────────────

func identityAwsAuthDiffHandler(reg *clusters.Registry, awsCfg aws.Config, emitter *audit.Emitter) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		if !c.EKSCapable() {
			writeAPIErrorJSON(w, http.StatusUnprocessableEntity, errBackendNotEKSCode,
				"identity is only available for EKS-backed clusters")
			return
		}

		// Per-request K8s clientset uses the user's impersonation
		// so RBAC denials at the K8s layer surface naturally.
		cs, err := k8s.NewClientset(r.Context(), p, c)
		if err != nil {
			writeAPIErrorJSON(w, http.StatusInternalServerError, "E_K8S_CLIENT",
				"failed to build k8s clientset: "+err.Error())
			return
		}

		// Step 1: read the aws-auth ConfigMap. 404 is the desired
		// migration-complete signal — render an empty parsed list,
		// don't error.
		cm, err := cs.CoreV1().ConfigMaps(identity.AwsAuthConfigMapNamespace).Get(r.Context(), identity.AwsAuthConfigMapName, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			emitIdentityRead(r.Context(), emitter, c, audit.OutcomeFailure, "read_aws_auth", err.Error())
			if errors.Is(err, context.Canceled) {
				return
			}
			writeAPIErrorJSON(w, http.StatusBadGateway, "E_K8S_API",
				"failed to read aws-auth ConfigMap: "+err.Error())
			return
		}
		emitIdentityRead(r.Context(), emitter, c, audit.OutcomeSuccess, "read_aws_auth", "")

		authEntries, perr := identity.ParseAwsAuth(cm)
		if perr != nil {
			writeAPIErrorJSON(w, http.StatusBadGateway, "E_AWS_AUTH_PARSE",
				"failed to parse aws-auth ConfigMap: "+perr.Error())
			return
		}

		// Step 2: list + describe access entries with the same
		// fan-out as the dedicated handler.
		client := newIdentityClient(awsCfg, c)
		eksName := c.EKSName()
		principalARNs, err := client.ListAccessEntries(r.Context(), eksName)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			emitIdentityRead(r.Context(), emitter, c, audit.OutcomeFailure, "list_access_entries", err.Error())
			status, code := awsErrorToStatus(err)
			writeAPIErrorJSON(w, status, code, "failed to list access entries: "+err.Error())
			return
		}
		emitIdentityRead(r.Context(), emitter, c, audit.OutcomeSuccess, "list_access_entries", "")

		entries := make([]identity.AccessEntry, len(principalARNs))
		sem := make(chan struct{}, identityFanoutLimit)
		var wg sync.WaitGroup
		for i, arn := range principalARNs {
			wg.Add(1)
			sem <- struct{}{}
			go func(i int, arn string) {
				defer wg.Done()
				defer func() { <-sem }()
				ent, derr := client.DescribeAccessEntry(r.Context(), eksName, arn)
				if derr != nil {
					emitIdentityRead(r.Context(), emitter, c, audit.OutcomeFailure, "describe_access_entry", derr.Error())
					return
				}
				emitIdentityRead(r.Context(), emitter, c, audit.OutcomeSuccess, "describe_access_entry", "")
				entries[i] = ent
			}(i, arn)
		}
		wg.Wait()

		// Step 3: pure-logic diff.
		diff, health := identity.DiffAwsAuthVsAccessEntries(authEntries, entries)
		writeJSON(w, http.StatusOK, identity.AwsAuthDiffResponse{Entries: diff, Health: health})
	}
}

// ── /identity/sa-roles ──────────────────────────────────────────

func identitySARolesHandler(reg *clusters.Registry, cache *identityCache, emitter *audit.Emitter) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		if !c.EKSCapable() {
			writeAPIErrorJSON(w, http.StatusUnprocessableEntity, errBackendNotEKSCode,
				"identity is only available for EKS-backed clusters")
			return
		}

		mgr, err := cache.For(r.Context(), c)
		if err != nil {
			emitIdentityRead(r.Context(), emitter, c, audit.OutcomeFailure, "sa_roles_setup", err.Error())
			writeAPIErrorJSON(w, http.StatusInternalServerError, "E_IDENTITY_SETUP",
				"failed to set up identity manager: "+err.Error())
			return
		}

		entries, err := mgr.Ensure(r.Context())
		if err != nil {
			if errors.Is(err, identity.ErrIRSAListerNotReady) {
				// Informer still syncing — give SPA a Retry-After.
				w.Header().Set("Retry-After", "3")
				writeAPIErrorJSON(w, http.StatusServiceUnavailable, "E_IDENTITY_WARMING",
					"identity informer is still syncing; retry shortly")
				return
			}
			emitIdentityRead(r.Context(), emitter, c, audit.OutcomeFailure, "ensure_sa_roles", err.Error())
			if errors.Is(err, context.Canceled) {
				return
			}
			// Partial-failure: Ensure() may return both a stale
			// snapshot AND an error. If entries is non-empty, render
			// them with a warning header so the SPA can show a chip.
			if len(entries) > 0 {
				w.Header().Set("X-Identity-Stale", "true")
				writeJSON(w, http.StatusOK, entries)
				return
			}
			status, code := awsErrorToStatus(err)
			writeAPIErrorJSON(w, status, code, "failed to build SA→Role index: "+err.Error())
			return
		}
		emitIdentityRead(r.Context(), emitter, c, audit.OutcomeSuccess, "ensure_sa_roles", "")

		writeJSON(w, http.StatusOK, entries)
	}
}

// ── /identity/pod-identity ──────────────────────────────────────

// identityPodIdentityResponse is the wire shape: role ARN → list of
// (namespace, SA) pairs.
type identityPodIdentityResponse struct {
	Groups map[string][]identity.PodIdentityAssoc `json:"groups"`
}

func identityPodIdentityHandler(reg *clusters.Registry, awsCfg aws.Config, emitter *audit.Emitter) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		c, ok := reg.ByName(chi.URLParam(r, "cluster"))
		if !ok {
			http.Error(w, "cluster not found", http.StatusNotFound)
			return
		}
		if !c.EKSCapable() {
			writeAPIErrorJSON(w, http.StatusUnprocessableEntity, errBackendNotEKSCode,
				"identity is only available for EKS-backed clusters")
			return
		}

		client := newIdentityClient(awsCfg, c)
		eksName := c.EKSName()
		assocs, err := client.ListPodIdentityAssociations(r.Context(), eksName)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return
			}
			emitIdentityRead(r.Context(), emitter, c, audit.OutcomeFailure, "list_pod_identity", err.Error())
			status, code := awsErrorToStatus(err)
			writeAPIErrorJSON(w, status, code, "failed to list pod identity associations: "+err.Error())
			return
		}
		emitIdentityRead(r.Context(), emitter, c, audit.OutcomeSuccess, "list_pod_identity", "")

		writeJSON(w, http.StatusOK, identityPodIdentityResponse{
			Groups: identity.GroupPodIdentityByRole(assocs),
		})
	}
}

// identityHandlerDeadline caps each handler's total budget. Keeps a
// misbehaving region from holding an SSE-adjacent route open.
const identityHandlerDeadline = 20 * time.Second

// withIdentityDeadline wraps r.Context() with a hard deadline. Used
// when registering routes in main.go.
func withIdentityDeadline(h func(http.ResponseWriter, *http.Request, credentials.Provider)) func(http.ResponseWriter, *http.Request, credentials.Provider) {
	return func(w http.ResponseWriter, r *http.Request, p credentials.Provider) {
		ctx, cancel := context.WithTimeout(r.Context(), identityHandlerDeadline)
		defer cancel()
		h(w, r.WithContext(ctx), p)
	}
}

