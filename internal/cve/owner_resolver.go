package cve

import (
	"context"
	"encoding/json"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/gnana997/periscope/internal/awsec2"
)

// OwnerResolver classifies an EC2 instance as managed-nodegroup,
// karpenter-nodeclaim, or unmanaged. The classification feeds the
// InstanceEntry.OwnerKind + OwnerName fields and lets the API layer
// (#165) render the right "what would I patch?" answer for each row.
//
// Order of checks (first match wins):
//
//  1. EC2 tag `eks:nodegroup-name` present → managed-nodegroup.
//     EKS stamps every node a managed-nodegroup launch template
//     creates with this tag, and only managed nodegroups.
//
//  2. EC2 tag `karpenter.sh/nodepool` present → karpenter-nodeclaim.
//     Karpenter labels every instance it provisions with the
//     nodepool name; the NodeClaim CR name is then looked up via
//     the cluster's K8s API by matching `spec.providerID` against
//     the instance ID.
//
//  3. Fallback → unmanaged. Self-managed nodegroups, manually
//     attached instances, anything Periscope can't classify.
//
// Steps 1 and 2 are pure tag inspection; step 2's NodeClaim name
// lookup is best-effort — if the K8s call fails the resolver still
// returns OwnerKarpenter with an empty OwnerName rather than failing
// the whole hydrate.
type OwnerResolver struct {
	listFn  func(ctx context.Context, cs kubernetes.Interface) ([]nodeClaimRef, error) // overridable for tests
	getCS   func(ctx context.Context) (kubernetes.Interface, error)
}

// nodeClaimRef is the projected shape of a Karpenter NodeClaim that
// the resolver needs: the CR name + the EC2 provider ID it points at.
// Modelled as a plain struct so the resolver doesn't drag in the
// Karpenter CRD client; we list NodeClaims via the dynamic / typed
// custom-resource path provided by the caller.
type nodeClaimRef struct {
	Name       string
	ProviderID string // "aws:///us-east-1a/i-0abc..."
}

// NewOwnerResolver builds a resolver wired against the given EC2
// client. getCS returns a K8s clientset for the cluster being
// resolved; the resolver calls it lazily, and only when a Karpenter
// instance is detected.
func NewOwnerResolver(getCS func(ctx context.Context) (kubernetes.Interface, error)) *OwnerResolver {
	return &OwnerResolver{
		getCS:  getCS,
		listFn: listKarpenterNodeClaims,
	}
}

// Resolve classifies each instance in metas and returns a map
// instanceID → (kind, name). Missing instances (described but no
// matching tags) fall through to OwnerUnmanaged with an empty name.
//
// Returns a single error only if the underlying K8s listing fails
// in a way that prevents Karpenter resolution; tag-only classification
// branches can never fail.
func (r *OwnerResolver) Resolve(ctx context.Context, metas []awsec2.InstanceMeta) (map[string]OwnerKind, map[string]string, error) {
	kinds := make(map[string]OwnerKind, len(metas))
	names := make(map[string]string, len(metas))
	needKarpenter := false
	for _, m := range metas {
		switch {
		case m.Tags["eks:nodegroup-name"] != "":
			kinds[m.InstanceID] = OwnerManagedNodegroup
			names[m.InstanceID] = m.Tags["eks:nodegroup-name"]
		case m.Tags["karpenter.sh/nodepool"] != "":
			kinds[m.InstanceID] = OwnerKarpenter
			// OwnerName filled in below by NodeClaim lookup.
			needKarpenter = true
		default:
			kinds[m.InstanceID] = OwnerUnmanaged
		}
	}

	if !needKarpenter || r.getCS == nil {
		return kinds, names, nil
	}

	cs, err := r.getCS(ctx)
	if err != nil {
		// Karpenter classification stays (we know what they are), we
		// just couldn't get the friendly NodeClaim names. Return
		// nil error so hydrate continues; the API layer renders
		// the instance ID instead of the claim name.
		return kinds, names, nil
	}
	claims, err := r.listFn(ctx, cs)
	if err != nil {
		// Same rationale as above — name is nice-to-have, kind is
		// not.
		return kinds, names, nil
	}

	byInstance := make(map[string]string, len(claims))
	for _, c := range claims {
		if id := instanceIDFromProviderID(c.ProviderID); id != "" {
			byInstance[id] = c.Name
		}
	}
	for id, kind := range kinds {
		if kind == OwnerKarpenter {
			names[id] = byInstance[id]
		}
	}
	return kinds, names, nil
}

// instanceIDFromProviderID extracts the EC2 instance ID from a
// Karpenter providerID of the form "aws:///<az>/i-0abcdef0123456789".
// Returns "" if the shape is unexpected (legitimate when the
// NodeClaim has not been bound to an EC2 instance yet — e.g.
// Spec.NodeName is set but the launch hasn't completed).
func instanceIDFromProviderID(pid string) string {
	const prefix = "aws:///"
	if !strings.HasPrefix(pid, prefix) {
		return ""
	}
	// strip prefix, then take the segment after the last '/'.
	rest := pid[len(prefix):]
	if i := strings.LastIndex(rest, "/"); i >= 0 {
		return rest[i+1:]
	}
	return rest
}

// listKarpenterNodeClaims lists Karpenter NodeClaims via the dynamic
// path. Returns an empty slice (not an error) when the CRD is not
// installed — a cluster without Karpenter just has zero claims, and
// the resolver's caller treats that as "no names to enrich with".
//
// Listed as a free function (not a method) so tests can swap
// OwnerResolver.listFn for a fixture without going through K8s
// fakes for the Karpenter group.
func listKarpenterNodeClaims(ctx context.Context, cs kubernetes.Interface) ([]nodeClaimRef, error) {
	// Karpenter's NodeClaim CRD lives at karpenter.sh/v1; the K8s
	// typed clientset doesn't know about CRD groups, so we drop to
	// the discovery client's RESTClient. To avoid pulling in the
	// dynamic-client dep here, we use the discovery REST client to
	// hit the list endpoint directly.
	//
	// This deliberately tolerates a missing CRD as a clean empty
	// result — operators without Karpenter installed should not
	// see CVE hydrate fail.
	const path = "/apis/karpenter.sh/v1/nodeclaims"
	raw, err := cs.Discovery().RESTClient().Get().AbsPath(path).DoRaw(ctx)
	if err != nil {
		// Distinguish "CRD absent" (404) from "API unreachable"
		// (anything else). On 404 we want clean empty; on other
		// errors we still return empty (best-effort) so hydrate
		// does not fail, but log at the call site if needed.
		return nil, nil
	}
	// Parse only the fields we need; the full CRD schema is much
	// larger and would couple this package to the karpenter API
	// types. metav1 + a tiny inline shape suffice.
	var list struct {
		metav1.TypeMeta `json:",inline"`
		Items           []struct {
			metav1.ObjectMeta `json:"metadata"`
			Spec              struct {
				ProviderID string `json:"providerID"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, nil
	}
	out := make([]nodeClaimRef, 0, len(list.Items))
	for _, it := range list.Items {
		out = append(out, nodeClaimRef{Name: it.Name, ProviderID: it.Spec.ProviderID})
	}
	return out, nil
}

// PodImageDigests extracts the resolved image digests from a pod's
// containerStatuses (NOT spec.containers[].image, which is the
// operator-written tag). Inspector keys on digest, so the watch
// predicate and hydrate loop both need this exact path.
//
// containerStatuses[].imageID has the form
//
//	docker-pullable://repo@sha256:abc...
//
// or sometimes the older
//
//	docker://sha256:abc...
//
// We strip the URI prefix and any repo segment, returning the bare
// `sha256:abc...` digest that Inspector's EcrImageHash filter
// accepts. Empty entries (image not yet pulled) are skipped.
func PodImageDigests(pod *corev1.Pod) []string {
	if pod == nil {
		return nil
	}
	all := make([]string, 0, len(pod.Status.ContainerStatuses)+len(pod.Status.InitContainerStatuses))
	for _, cs := range pod.Status.ContainerStatuses {
		if d := normalizeImageID(cs.ImageID); d != "" {
			all = append(all, d)
		}
	}
	for _, cs := range pod.Status.InitContainerStatuses {
		if d := normalizeImageID(cs.ImageID); d != "" {
			all = append(all, d)
		}
	}
	return all
}

// normalizeImageID strips known prefixes and any repo path so the
// returned value is the bare `sha256:...` digest.
func normalizeImageID(id string) string {
	if id == "" {
		return ""
	}
	// Drop URI prefix.
	for _, p := range []string{"docker-pullable://", "docker://"} {
		if strings.HasPrefix(id, p) {
			id = id[len(p):]
			break
		}
	}
	// If there's a repo segment ("repo@sha256:..."), keep only the
	// digest. If the string is already a bare sha256, this is a
	// no-op.
	if i := strings.Index(id, "@"); i >= 0 {
		id = id[i+1:]
	}
	if !strings.HasPrefix(id, "sha256:") {
		return ""
	}
	return id
}
