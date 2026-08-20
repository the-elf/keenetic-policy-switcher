package keenetic

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// Policy is a connection policy available to assign to a device.
type Policy struct {
	ID   string // "default", or the router's internal name, e.g. "Policy0"
	Name string // human-readable description from the router admin panel
}

// policyEntry mirrors one value in GET /rci/show/rc/ip/policy, keyed by the
// router's internal PolicyN name.
type policyEntry struct {
	Description string `json:"description"`
}

// ListPolicies reads the router's connection policies and their
// human-readable descriptions, prefixed with the synthetic "default" policy
// used for hosts with no explicit assignment.
func (c *Client) ListPolicies(ctx context.Context) ([]Policy, error) {
	body, err := c.get(ctx, "/rci/show/rc/ip/policy")
	if err != nil {
		return nil, err
	}

	var raw map[string]policyEntry
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("keenetic: parsing /rci/show/rc/ip/policy: %w", err)
	}

	return buildPolicyList(raw), nil
}

// buildPolicyList turns the router's PolicyN -> description map into an
// ordered list, prefixed with the synthetic "default" policy.
func buildPolicyList(raw map[string]policyEntry) []Policy {
	ids := make([]string, 0, len(raw))
	for id := range raw {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	policies := make([]Policy, 0, len(ids)+1)
	policies = append(policies, Policy{ID: DefaultPolicyID, Name: "Default"})
	for _, id := range ids {
		policies = append(policies, Policy{ID: id, Name: raw[id].Description})
	}
	return policies
}
