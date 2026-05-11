// karpenter_event_parser.go — extract per-NodePool incompatibility
// reasons from a FailedScheduling apiserver Event message.
//
// Karpenter's scheduler emits a single Event per pending pod with
// reason="FailedScheduling" and a Message containing the full
// per-NodePool aggregation. The relevant wrap pattern in upstream
// Karpenter is:
//
//   serrors.Wrap(err, "NodePool", klog.KRef("", nodePoolName))
//
// which renders into the Event message as `NodePool="<name>"` with
// the rejection reason adjacent. Multi-pool messages concatenate
// these segments via `multierr`.
//
// The parser is best-effort: it pulls out (NodePool, reason) pairs
// when the format matches and returns an empty slice otherwise. The
// caller renders the raw Event message to the operator in either
// case, so a parse miss degrades to "still useful" rather than
// "broken UI."

package k8s

import (
	"regexp"
	"strings"
)

// nodePoolMarkerRE matches the rendered serrors.Wrap output.
// Karpenter renders klog.KRef("", name) as `NodePool="<name>"` (or
// `NodePool=<name>` in some logr backends). We accept both quoting
// styles. The capture group is the pool name; everything after the
// match (until the next NodePool= boundary or end-of-string) is the
// associated rejection reason.
var nodePoolMarkerRE = regexp.MustCompile(`NodePool="?([A-Za-z0-9][-A-Za-z0-9_.]*)"?`)

// parseFailedSchedulingEvent extracts per-NodePool incompatibility
// reasons from a FailedScheduling Event message. Returns nil when
// the message doesn't carry the Karpenter wrap convention (e.g.
// kube-scheduler-style "0/3 nodes are available" messages).
func parseFailedSchedulingEvent(message string) []NodePoolIncompat {
	if message == "" {
		return nil
	}
	matches := nodePoolMarkerRE.FindAllStringSubmatchIndex(message, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]NodePoolIncompat, 0, len(matches))
	// Karpenter renders serrors.Wrap as `<reason text> NodePool="<name>"`
	// — the marker is AFTER the reason. For each match, the reason
	// is the text from the previous matchs end (or string start) to
	// THIS matchs start, with surrounding punctuation trimmed.
	prev := 0
	for _, m := range matches {
		start, end := m[0], m[1]
		// Submatch indices: m[2..3] = capture group 1 (pool name).
		pool := message[m[2]:m[3]]
		reason := strings.TrimSpace(message[prev:start])
		reason = strings.TrimLeft(reason, ":,;.- \t")
		reason = strings.TrimRight(reason, ":,;. \t")
		reason = strings.Join(strings.Fields(reason), " ")
		prev = end
		if reason == "" {
			continue
		}
		out = append(out, NodePoolIncompat{NodePool: pool, Reason: reason})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
