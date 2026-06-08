package awsssm

import "testing"

func TestInstanceIDFromProviderID(t *testing.T) {
	ok := map[string]string{
		"aws:///us-east-1a/i-0542b1a3d47e76f48": "i-0542b1a3d47e76f48", // 17-char
		"aws:///eu-west-1b/i-0abc1234":          "i-0abc1234",          // 8-char
		"aws:///i-0542b1a3d47e76f48":            "i-0542b1a3d47e76f48", // no AZ segment
	}
	for in, want := range ok {
		got, err := InstanceIDFromProviderID(in)
		if err != nil || got != want {
			t.Errorf("InstanceIDFromProviderID(%q) = (%q, %v), want (%q, nil)", in, got, err, want)
		}
	}

	bad := []string{
		"",
		"gce://project/zone/instance",
		"aws:///us-east-1a/notaninstance",
		"aws:///us-east-1a/i-0542b1a3d47e76f48/extra",
		"i-0542b1a3d47e76f48",
	}
	for _, in := range bad {
		if got, err := InstanceIDFromProviderID(in); err == nil {
			t.Errorf("InstanceIDFromProviderID(%q) = %q, want error", in, got)
		}
	}
}
