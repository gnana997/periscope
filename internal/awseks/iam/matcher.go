package iam

import "strings"

// matchAction reports whether the IAM action `target` matches the
// glob `pattern`. Case-insensitive (IAM evaluation is case-
// insensitive). Glob metacharacters:
//
//   *  matches any sequence of characters (including none)
//   ?  matches any single character
//
// IAM actions never contain "/", so the cross-"/" question doesn't
// arise here — but the underlying matcher is the same shared with
// matchResource, where it does matter.
func matchAction(pattern, target string) bool {
	return iamGlobMatch(pattern, target)
}

// matchResource reports whether the IAM resource ARN `target`
// matches the glob `pattern`. Case-insensitive (per AWS docs,
// "wildcards in ARNs are case-insensitive when matching").
//
// CRITICAL: unlike Go's path.Match, "*" here matches any character
// INCLUDING "/". The pattern "arn:aws:s3:::bucket/*" must match
// "arn:aws:s3:::bucket/path/to/file.txt" at any nesting depth —
// that's the documented IAM evaluator behavior.
//
// Partition is part of the ARN string and matched literally, so
// "arn:aws:..." patterns do NOT match "arn:aws-us-gov:..." targets
// and vice versa.
func matchResource(pattern, target string) bool {
	return iamGlobMatch(pattern, target)
}

// EvaluateEffect determines the effective allow/deny decision for
// a set of matching Permissions per IAM evaluation logic:
//
//   - Any explicit Deny in the set → denied.
//   - No Deny, at least one Allow → allowed.
//   - No matching rows at all → denied (implicit deny).
//
// Called by the reverse-lookup result resolver to decide whether
// a (SA, action, resource) combination is effectively allowed
// after consolidating all matching policy rows. Tests use this
// helper directly; engine.go consumes it post-matcher to compute
// the "effective" badge on each result row.
func EvaluateEffect(perms []Permission) bool {
	var hasAllow bool
	for _, p := range perms {
		if p.Effect == EffectDeny {
			return false // explicit Deny short-circuits per IAM semantics
		}
		if p.Effect == EffectAllow {
			hasAllow = true
		}
	}
	return hasAllow
}

// iamGlobMatch is the shared glob matcher for both actions and
// resource ARNs. Case-insensitive. Iterative algorithm with O(n*m)
// worst case but linear on every input we've seen in practice —
// pathological patterns like "*a*b*c*d*..." against long targets
// would degrade, but IAM glob patterns are short by spec (<256
// chars).
//
// Unlike Go's path.Match / filepath.Match, "*" here matches any
// character including "/" because IAM evaluator semantics ignore
// "/" as a path separator in resource ARNs.
func iamGlobMatch(pattern, target string) bool {
	p := strings.ToLower(pattern)
	t := strings.ToLower(target)
	return globMatchLower(p, t)
}

// globMatchLower assumes pattern and target are already lower-cased.
// Classic iterative wildcard matcher with backtracking on "*".
//
// pi, ti  — current indices in pattern / target
// starPi  — index of the most recent "*" in pattern (-1 if none)
// starTi  — target index when the most recent "*" was opened
func globMatchLower(pattern, target string) bool {
	pi, ti := 0, 0
	starPi, starTi := -1, 0
	for ti < len(target) {
		if pi < len(pattern) {
			switch pattern[pi] {
			case '*':
				// Record backtrack point; advance pattern but not target.
				starPi = pi
				starTi = ti
				pi++
				continue
			case '?':
				pi++
				ti++
				continue
			}
			if pattern[pi] == target[ti] {
				pi++
				ti++
				continue
			}
		}
		// Mismatch — backtrack to the last "*" if one exists.
		if starPi != -1 {
			pi = starPi + 1
			starTi++
			ti = starTi
			continue
		}
		return false
	}
	// Consume any trailing "*"s in the pattern.
	for pi < len(pattern) && pattern[pi] == '*' {
		pi++
	}
	return pi == len(pattern)
}
