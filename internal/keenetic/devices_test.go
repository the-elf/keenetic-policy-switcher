package keenetic

import (
	"encoding/json"
	"testing"
)

func TestMergeDevices_FromFixtures(t *testing.T) {
	var statuses []hostStatus
	if err := json.Unmarshal(readTestdata(t, "hotspot-host.json"), &statuses); err != nil {
		t.Fatalf("parsing hotspot-host.json: %v", err)
	}
	var configs []hostConfig
	if err := json.Unmarshal(readTestdata(t, "rc-ip-hotspot-host.json"), &configs); err != nil {
		t.Fatalf("parsing rc-ip-hotspot-host.json: %v", err)
	}

	devices := mergeDevices(statuses, configs)
	if len(devices) != len(statuses) {
		t.Fatalf("mergeDevices returned %d devices, expected %d", len(devices), len(statuses))
	}

	byMAC := make(map[string]Device, len(devices))
	for _, d := range devices {
		byMAC[d.MAC] = d
	}

	// a4:c3:65:12:34:8f — online, explicit Policy0.
	got, ok := byMAC["a4:c3:65:12:34:8f"]
	if !ok {
		t.Fatal("a4:c3:65:12:34:8f not found in the result")
	}
	if !got.Online || got.PolicyID != "Policy0" || got.IP != "192.168.1.42" {
		t.Errorf("a4:c3:65:12:34:8f: %+v", got)
	}

	// c2:e0:fd:85:c5:91 (the TV) — present in status but has no rc-config
	// entry at all -> should get the default policy.
	got, ok = byMAC["c2:e0:fd:85:c5:91"]
	if !ok {
		t.Fatal("c2:e0:fd:85:c5:91 not found in the result")
	}
	if got.PolicyID != DefaultPolicyID {
		t.Errorf("the TV with no explicit policy in the config should get %q, got %q", DefaultPolicyID, got.PolicyID)
	}

	// 5c:e9:1e:a3:df:85 — offline, has an rc entry but no policy field
	// (permit only) -> also the default policy.
	got, ok = byMAC["5c:e9:1e:a3:df:85"]
	if !ok {
		t.Fatal("5c:e9:1e:a3:df:85 not found in the result")
	}
	if got.Online {
		t.Error("5c:e9:1e:a3:df:85 should be offline")
	}
	if got.PolicyID != DefaultPolicyID {
		t.Errorf("a host with an rc entry but no policy should get %q, got %q", DefaultPolicyID, got.PolicyID)
	}
	if got.IP != "0.0.0.0" || got.Name != "5c:e9:1e:a3:df:85" {
		t.Errorf("offline host: unexpected IP/Name: %+v", got)
	}
}
