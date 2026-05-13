package identity

import "sort"

// SAKey is the canonical key for a ServiceAccount in the SA↔Role
// index: namespace + name. Used as a map key in UnifySARoles input
// and returned in the index entries.
type SAKey struct {
	Namespace string
	Name      string
}

// UnifySARoles merges IRSA annotations and Pod Identity associations
// into one row per ServiceAccount. Both inputs are keyed naturally:
//
//   - irsa maps SAKey → role ARN from the SA's
//     eks.amazonaws.com/role-arn annotation (empty value means the
//     annotation is absent on that SA).
//   - podIdentity is the full list of associations returned by
//     ListPodIdentityAssociations for the cluster.
//   - roleExists is a lookup of normalized role ARN → exists flag,
//     populated by iam:GetRole probes. ARNs missing from the map
//     default to false (we couldn't verify).
//
// DualSource is set when a single SA has *both* an IRSA annotation
// AND a Pod Identity association, regardless of whether the role
// ARNs match — operators care about both forms of dead config.
//
// Output is sorted by (namespace, saName) for deterministic
// rendering and test snapshots.
func UnifySARoles(cluster string, irsa map[SAKey]string, podIdentity []PodIdentityAssoc, roleExists map[string]bool) []SARoleIndexEntry {
	if roleExists == nil {
		roleExists = map[string]bool{}
	}

	// Bucket Pod Identity associations by SAKey. A single SA may
	// have multiple associations (legal in EKS); we keep them all.
	piBySA := map[SAKey][]PodIdentityAssoc{}
	for _, a := range podIdentity {
		k := SAKey{Namespace: a.Namespace, Name: a.ServiceAccount}
		piBySA[k] = append(piBySA[k], a)
	}

	// Build the entry set keyed by SAKey from the union of IRSA
	// keys and Pod-Identity keys. SAs that appear only in irsa
	// with an empty annotation value (i.e. no role configured)
	// are dropped — they have nothing to show on the page.
	keys := map[SAKey]struct{}{}
	for k, role := range irsa {
		if role == "" {
			continue
		}
		keys[k] = struct{}{}
	}
	for k := range piBySA {
		keys[k] = struct{}{}
	}

	entries := make([]SARoleIndexEntry, 0, len(keys))
	for k := range keys {
		entry := SARoleIndexEntry{
			Cluster:   cluster,
			Namespace: k.Namespace,
			SAName:    k.Name,
		}

		irsaRole, irsaPresent := irsa[k]
		irsaPresent = irsaPresent && irsaRole != ""
		piAssocs := piBySA[k]

		if irsaPresent {
			entry.Bindings = append(entry.Bindings, SARoleBinding{
				Source:              SourceIRSA,
				RoleArn:             irsaRole,
				RoleExists:          roleExists[NormalizePrincipalArn(irsaRole)],
				IRSAAnnotationValue: irsaRole,
			})
		}
		for _, a := range piAssocs {
			entry.Bindings = append(entry.Bindings, SARoleBinding{
				Source:                   SourcePodIdentity,
				RoleArn:                  a.RoleArn,
				RoleExists:               roleExists[NormalizePrincipalArn(a.RoleArn)],
				PodIdentityAssociationId: a.AssociationId,
			})
		}

		entry.DualSource = irsaPresent && len(piAssocs) > 0
		entries = append(entries, entry)
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Namespace != entries[j].Namespace {
			return entries[i].Namespace < entries[j].Namespace
		}
		return entries[i].SAName < entries[j].SAName
	})

	return entries
}

// GroupPodIdentityByRole renders the role-centric view of Pod
// Identity associations for section 3 of the Identity page. Each
// role ARN maps to the (namespace, ServiceAccount) pairs that bind
// to it. Output value slices are sorted by (namespace, sa) for
// deterministic rendering.
func GroupPodIdentityByRole(assocs []PodIdentityAssoc) map[string][]PodIdentityAssoc {
	out := map[string][]PodIdentityAssoc{}
	for _, a := range assocs {
		out[a.RoleArn] = append(out[a.RoleArn], a)
	}
	for k := range out {
		v := out[k]
		sort.Slice(v, func(i, j int) bool {
			if v[i].Namespace != v[j].Namespace {
				return v[i].Namespace < v[j].Namespace
			}
			return v[i].ServiceAccount < v[j].ServiceAccount
		})
	}
	return out
}
