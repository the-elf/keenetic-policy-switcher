package keenetic

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// Device is a normalized registered host, merged from the router's live
// hotspot status and its running-config policy assignment.
type Device struct {
	MAC      string
	Name     string
	IP       string
	Online   bool
	PolicyID string // "default" when the host has no explicit policy assigned
}

// hostStatus mirrors the fields we use from GET /rci/show/ip/hotspot/host.
// That endpoint has no "policy" field at all — see docs/api-notes.md.
type hostStatus struct {
	MAC    string `json:"mac"`
	Name   string `json:"name"`
	IP     string `json:"ip"`
	Active bool   `json:"active"`
}

// hostConfig mirrors GET /rci/show/rc/ip/hotspot/host: the running-config
// entry for a host, present only for hosts that ever got an explicit
// permit/policy write. "Policy" is empty when the host has no explicit
// policy (i.e. it runs on the router's default policy).
type hostConfig struct {
	MAC    string `json:"mac"`
	Permit bool   `json:"permit"`
	Policy string `json:"policy"`
}

// DefaultPolicyID is the synthetic policy id used both for hosts with no
// explicit policy assignment and for resetting a host to the router's
// default policy.
const DefaultPolicyID = "default"

// ListDevices reads and normalizes the list of registered hosts, with each
// host's current policy resolved from running-config (see
// docs/api-notes.md — the live status endpoint doesn't carry it).
func (c *Client) ListDevices(ctx context.Context) ([]Device, error) {
	statusBody, err := c.get(ctx, "/rci/show/ip/hotspot/host")
	if err != nil {
		return nil, err
	}
	var statuses []hostStatus
	if err := json.Unmarshal(statusBody, &statuses); err != nil {
		return nil, fmt.Errorf("keenetic: parsing /rci/show/ip/hotspot/host: %w", err)
	}

	configBody, err := c.get(ctx, "/rci/show/rc/ip/hotspot/host")
	if err != nil {
		return nil, err
	}
	var configs []hostConfig
	if err := json.Unmarshal(configBody, &configs); err != nil {
		return nil, fmt.Errorf("keenetic: parsing /rci/show/rc/ip/hotspot/host: %w", err)
	}

	return mergeDevices(statuses, configs), nil
}

// mergeDevices combines the live hotspot status list (name/ip/online) with
// the running-config policy assignments (policy/permit, keyed by MAC, only
// present for hosts that were ever explicitly configured). A host missing
// from configs, or present but without a "policy" field, runs on the
// router's default policy.
func mergeDevices(statuses []hostStatus, configs []hostConfig) []Device {
	policyByMAC := make(map[string]string, len(configs))
	for _, cfg := range configs {
		if cfg.Policy != "" {
			policyByMAC[normalizeMAC(cfg.MAC)] = cfg.Policy
		}
	}

	devices := make([]Device, 0, len(statuses))
	for _, s := range statuses {
		mac := normalizeMAC(s.MAC)
		policyID := DefaultPolicyID
		if p, ok := policyByMAC[mac]; ok {
			policyID = p
		}
		devices = append(devices, Device{
			MAC:      mac,
			Name:     s.Name,
			IP:       s.IP,
			Online:   s.Active,
			PolicyID: policyID,
		})
	}

	sort.Slice(devices, func(i, j int) bool {
		return devices[i].Name < devices[j].Name
	})
	return devices
}
