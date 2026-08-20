package keenetic

import (
	"encoding/json"
	"fmt"
	"strings"
)

// rciStatus is one entry of an RCI "status" array, e.g.
// {"status":"error","code":"...","ident":"...","message":"..."}.
type rciStatus struct {
	Status  string
	Code    string
	Ident   string
	Message string
}

// checkRCIErrors parses a raw RCI response body and returns an error if any
// nested "status" entry has status != "message" (the router answers writes
// with HTTP 200 even when a command failed — the failure is only visible in
// the body, see docs/api-notes.md).
func checkRCIErrors(body []byte) error {
	var v interface{}
	if err := json.Unmarshal(body, &v); err != nil {
		return fmt.Errorf("keenetic: parsing router response: %w", err)
	}

	var errs []rciStatus
	collectStatusErrors(v, &errs)
	if len(errs) == 0 {
		return nil
	}

	msgs := make([]string, 0, len(errs))
	for _, e := range errs {
		msgs = append(msgs, fmt.Sprintf("%s: %s", e.Ident, e.Message))
	}
	return fmt.Errorf("router returned an error: %s", strings.Join(msgs, "; "))
}

func collectStatusErrors(v interface{}, out *[]rciStatus) {
	switch val := v.(type) {
	case map[string]interface{}:
		for key, sub := range val {
			if key == "status" {
				if arr, ok := sub.([]interface{}); ok {
					collectStatusArray(arr, out)
					continue
				}
			}
			collectStatusErrors(sub, out)
		}
	case []interface{}:
		for _, item := range val {
			collectStatusErrors(item, out)
		}
	}
}

func collectStatusArray(arr []interface{}, out *[]rciStatus) {
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		status, _ := m["status"].(string)
		if status == "" || status == "message" {
			continue
		}
		*out = append(*out, rciStatus{
			Status:  status,
			Code:    asString(m["code"]),
			Ident:   asString(m["ident"]),
			Message: asString(m["message"]),
		})
	}
}

func asString(v interface{}) string {
	s, _ := v.(string)
	return s
}
