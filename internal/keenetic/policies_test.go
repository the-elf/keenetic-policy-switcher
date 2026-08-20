package keenetic

import (
	"encoding/json"
	"testing"
)

func TestBuildPolicyList_FromFixture(t *testing.T) {
	var raw map[string]policyEntry
	if err := json.Unmarshal(readTestdata(t, "rc-ip-policy.json"), &raw); err != nil {
		t.Fatalf("parsing rc-ip-policy.json: %v", err)
	}

	policies := buildPolicyList(raw)
	if len(policies) != len(raw)+1 {
		t.Fatalf("buildPolicyList returned %d entries, expected %d (+1 default)", len(policies), len(raw)+1)
	}
	if policies[0].ID != DefaultPolicyID || policies[0].Name != "Default" {
		t.Errorf("first entry should be default, got %+v", policies[0])
	}

	byID := make(map[string]string, len(policies))
	for _, p := range policies {
		byID[p.ID] = p.Name
	}
	want := map[string]string{
		"Policy0": "With VPN",
		"Policy1": "ru_vpn",
		"Policy2": "No VPN",
	}
	for id, name := range want {
		if byID[id] != name {
			t.Errorf("policy %s: name = %q, expected %q", id, byID[id], name)
		}
	}
}
