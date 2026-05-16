package iam

import "sort"

// GroupByService bins permissions by their AWS service segment
// (the lower-cased prefix of the action before ":", e.g. "s3",
// "iam", "kms"). Wildcard-action statements (action="*") group
// under the service key "*".
//
// Per backend-as-source-of-truth (#188): the SPA renders one
// accordion per ServiceGroup and never re-buckets, so the same
// composition feeds an eventual MCP tool that wraps this endpoint.
//
// Ordering: groups sorted by (sensitive-first, then service alpha).
// Within each group, Permissions preserve whatever order the
// caller supplied — sortPermissions already imposes
// (Service, Action, Resource, Effect) on the engine output.
//
// Empty input returns an empty (non-nil) slice so callers can
// json.Marshal without re-checking nilness.
func GroupByService(perms []Permission) []ServiceGroup {
	if len(perms) == 0 {
		return []ServiceGroup{}
	}

	// Bucket by service, preserving insertion order within each
	// bucket (the input slice is already sorted by the engine).
	buckets := map[string][]Permission{}
	order := make([]string, 0, 16)
	for _, p := range perms {
		svc := p.Service
		if svc == "" {
			svc = "*"
		}
		if _, seen := buckets[svc]; !seen {
			order = append(order, svc)
		}
		buckets[svc] = append(buckets[svc], p)
	}

	groups := make([]ServiceGroup, 0, len(buckets))
	for _, svc := range order {
		bucket := buckets[svc]
		sensitive := false
		for _, p := range bucket {
			if p.Sensitive {
				sensitive = true
				break
			}
		}
		groups = append(groups, ServiceGroup{
			Service:     svc,
			Sensitive:   sensitive,
			Count:       len(bucket),
			Permissions: bucket,
		})
	}

	// Final group order: sensitive groups first, then alphabetical.
	// The wildcard service ("*") is treated as sensitive-aware via
	// its bucket's flag — no special-cased priority.
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Sensitive != groups[j].Sensitive {
			return groups[i].Sensitive // true first
		}
		return groups[i].Service < groups[j].Service
	})

	return groups
}
