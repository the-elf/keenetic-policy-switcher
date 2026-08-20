package keenetic

import (
	"encoding/json"
	"testing"
)

func TestSetPolicyCommands_ExplicitPolicy(t *testing.T) {
	cmds := setPolicyCommands("A4:C3:65:12:34:8F", "Policy0")
	raw, err := json.Marshal(cmds)
	if err != nil {
		t.Fatal(err)
	}

	var decoded []map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 2 {
		t.Fatalf("expected 2 commands (write + save), got %d", len(decoded))
	}

	host := dig(t, decoded[0], "ip", "hotspot", "host")
	if host["mac"] != "a4:c3:65:12:34:8f" {
		t.Errorf("mac not normalized: %v", host["mac"])
	}
	if host["permit"] != true {
		t.Errorf("permit = %v, expected true", host["permit"])
	}
	if host["policy"] != "Policy0" {
		t.Errorf("policy = %v, expected \"Policy0\"", host["policy"])
	}

	save := dig(t, decoded[1], "system", "configuration", "save")
	if save == nil {
		t.Error("second command should be system/configuration/save")
	}
}

func TestSetPolicyCommands_Default(t *testing.T) {
	cmds := setPolicyCommands("aa:bb:cc:dd:ee:ff", DefaultPolicyID)
	raw, err := json.Marshal(cmds)
	if err != nil {
		t.Fatal(err)
	}

	var decoded []map[string]interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}

	host := dig(t, decoded[0], "ip", "hotspot", "host")
	policy, ok := host["policy"].(map[string]interface{})
	if !ok {
		t.Fatalf("policy for default should be the object {\"no\":true}, got %#v", host["policy"])
	}
	if policy["no"] != true {
		t.Errorf(`policy = %v, expected {"no": true}`, policy)
	}
}

// dig walks a chain of nested map[string]interface{} keys, failing the test
// if any level is missing or not a map.
func dig(t *testing.T, m map[string]interface{}, keys ...string) map[string]interface{} {
	t.Helper()
	cur := m
	for _, k := range keys {
		next, ok := cur[k]
		if !ok {
			t.Fatalf("key %q is missing in %v", k, cur)
		}
		asMap, ok := next.(map[string]interface{})
		if !ok {
			t.Fatalf("value at key %q is not an object: %#v", k, next)
		}
		cur = asMap
	}
	return cur
}
