package awsssm

import (
	"fmt"
	"regexp"
)

// providerIDRe matches a Kubernetes Node's AWS providerID and captures
// the EC2 instance id. The canonical form is:
//
//	aws:///<availability-zone>/<instance-id>
//
// Some providers omit the AZ segment (aws:///<instance-id>); both are
// accepted. The instance id is i- followed by 8 or 17 hex digits.
var providerIDRe = regexp.MustCompile(`^aws://(?:/[^/]*)?/(i-[0-9a-f]{8}(?:[0-9a-f]{9})?)$`)

// InstanceIDFromProviderID parses an EC2 instance id out of a Node's
// spec.providerID. This is the gate for the node shell: a node is
// SSM-shellable iff it carries an AWS providerID, regardless of the
// cluster's Periscope backend (eks / agent / in-cluster) — SSM reaches
// the host through AWS, not the Kubernetes apiserver.
//
// Returns a typed error for empty or non-AWS providerIDs so callers can
// 404 cleanly ("this node is not an EC2 instance").
func InstanceIDFromProviderID(providerID string) (string, error) {
	if providerID == "" {
		return "", fmt.Errorf("node has no providerID")
	}
	m := providerIDRe.FindStringSubmatch(providerID)
	if m == nil {
		return "", fmt.Errorf("providerID %q is not an AWS EC2 instance", providerID)
	}
	return m[1], nil
}
