package keenetic

import (
	"context"
	"fmt"
)

// SetPolicy assigns policyID to the host identified by mac and persists the
// change, in a single batch request (policy write + configuration save) as
// recommended by the router — a separate save call risks losing the write
// on power loss between the two requests. policyID == DefaultPolicyID
// resets the host to the router's default policy (RCI's {"no": true}).
func (c *Client) SetPolicy(ctx context.Context, mac, policyID string) error {
	if !validMAC(mac) {
		return fmt.Errorf("keenetic: invalid MAC address %q", mac)
	}
	body, err := c.postRCI(ctx, setPolicyCommands(mac, policyID))
	if err != nil {
		return err
	}
	return checkRCIErrors(body)
}

// setPolicyCommands builds the batch body for SetPolicy: a host policy
// write followed by a configuration save, in one request (see
// docs/api-notes.md — this is the router-recommended, power-loss-safe
// shape). policyID == DefaultPolicyID resets to the router's default
// policy via RCI's {"no": true}.
func setPolicyCommands(mac, policyID string) []map[string]interface{} {
	var policyValue interface{} = policyID
	if policyID == DefaultPolicyID {
		policyValue = map[string]interface{}{"no": true}
	}

	return []map[string]interface{}{
		{
			"ip": map[string]interface{}{
				"hotspot": map[string]interface{}{
					"host": map[string]interface{}{
						"mac":    normalizeMAC(mac),
						"permit": true,
						"policy": policyValue,
					},
				},
			},
		},
		saveCommand(),
	}
}

// SetPermit blocks or allows internet access for the host, independent of
// its policy assignment, and persists the change.
func (c *Client) SetPermit(ctx context.Context, mac string, permit bool) error {
	if !validMAC(mac) {
		return fmt.Errorf("keenetic: invalid MAC address %q", mac)
	}
	commands := []map[string]interface{}{
		{
			"ip": map[string]interface{}{
				"hotspot": map[string]interface{}{
					"host": map[string]interface{}{
						"mac":    normalizeMAC(mac),
						"permit": permit,
					},
				},
			},
		},
		saveCommand(),
	}

	body, err := c.postRCI(ctx, commands)
	if err != nil {
		return err
	}
	return checkRCIErrors(body)
}

func saveCommand() map[string]interface{} {
	return map[string]interface{}{
		"system": map[string]interface{}{
			"configuration": map[string]interface{}{
				"save": map[string]interface{}{},
			},
		},
	}
}
