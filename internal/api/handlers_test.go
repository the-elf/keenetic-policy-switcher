package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockClient is a hand-rolled KeeneticClient double: fixed return values per
// method, so handler tests stay decoupled from the real keenetic package
// (spec §9.2 — handlers are tested "on top of a mock Keenetic client").
type mockClient struct {
	devices     []Device
	devicesErr  error
	policies    []Policy
	policiesErr error

	setPolicyErr error
	lastSetMAC   string
	lastSetID    string
}

func (m *mockClient) ListDevices(ctx context.Context) ([]Device, error) {
	return m.devices, m.devicesErr
}

func (m *mockClient) ListPolicies(ctx context.Context) ([]Policy, error) {
	return m.policies, m.policiesErr
}

func (m *mockClient) SetPolicy(ctx context.Context, mac, policyID string) error {
	m.lastSetMAC = mac
	m.lastSetID = policyID
	return m.setPolicyErr
}

func newTestServer(client KeeneticClient) http.Handler {
	mux := http.NewServeMux()
	NewHandler(client, nil).Register(mux)
	return mux
}

func TestHandleListDevices_Success(t *testing.T) {
	client := &mockClient{devices: []Device{
		{MAC: "a4:c3:65:12:34:8f", Name: "Laptop", IP: "192.168.1.42", Online: true, PolicyID: "Policy0"},
	}}
	srv := newTestServer(client)

	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, expected 200", rec.Code)
	}
	var got devicesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("parsing the response: %v", err)
	}
	if !got.RouterOnline {
		t.Error("router_online = false, expected true")
	}
	if len(got.Devices) != 1 || got.Devices[0].MAC != "a4:c3:65:12:34:8f" {
		t.Errorf("unexpected device list: %+v", got.Devices)
	}
}

func TestHandleListDevices_RouterOffline(t *testing.T) {
	client := &mockClient{devicesErr: errors.New("dial tcp: connection refused")}
	srv := newTestServer(client)

	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	// Router unreachability is not an HTTP error for this handler: the
	// frontend must get 200 with router_online:false, not a failed request.
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, expected 200 even with the router unreachable", rec.Code)
	}
	var got devicesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("parsing the response: %v", err)
	}
	if got.RouterOnline {
		t.Error("router_online = true on a client error, expected false")
	}
	if len(got.Devices) != 0 {
		t.Errorf("devices = %v, expected an empty list", got.Devices)
	}
}

func TestHandleListPolicies_Success(t *testing.T) {
	client := &mockClient{policies: []Policy{
		{ID: "default", Name: "Default"},
		{ID: "Policy0", Name: "Via VPN"},
	}}
	srv := newTestServer(client)

	req := httptest.NewRequest(http.MethodGet, "/api/policies", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, expected 200", rec.Code)
	}
	var got policiesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("parsing the response: %v", err)
	}
	if len(got.Policies) != 2 {
		t.Fatalf("policies = %v, expected 2 entries", got.Policies)
	}
}

func TestHandleListPolicies_RouterError(t *testing.T) {
	client := &mockClient{policiesErr: errors.New("boom")}
	srv := newTestServer(client)

	req := httptest.NewRequest(http.MethodGet, "/api/policies", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, expected 502", rec.Code)
	}
	assertErrorBody(t, rec.Body.Bytes())
}

func TestHandleSetPolicy_Success(t *testing.T) {
	client := &mockClient{}
	srv := newTestServer(client)

	body := bytes.NewBufferString(`{"policy_id":"Policy0"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/devices/a4:c3:65:12:34:8f/policy", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, expected 200, body: %s", rec.Code, rec.Body.String())
	}
	var got setPolicyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("parsing the response: %v", err)
	}
	if !got.OK || got.MAC != "a4:c3:65:12:34:8f" || got.PolicyID != "Policy0" {
		t.Errorf("unexpected response: %+v", got)
	}
	if client.lastSetMAC != "a4:c3:65:12:34:8f" || client.lastSetID != "Policy0" {
		t.Errorf("client called with mac=%q policy_id=%q", client.lastSetMAC, client.lastSetID)
	}
}

func TestHandleSetPolicy_RouterWriteError(t *testing.T) {
	client := &mockClient{setPolicyErr: errors.New("router rejected the write")}
	srv := newTestServer(client)

	body := bytes.NewBufferString(`{"policy_id":"Policy0"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/devices/a4:c3:65:12:34:8f/policy", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, expected 502", rec.Code)
	}
	assertErrorBody(t, rec.Body.Bytes())
}

func TestHandleSetPolicy_MissingPolicyID(t *testing.T) {
	client := &mockClient{}
	srv := newTestServer(client)

	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/api/devices/a4:c3:65:12:34:8f/policy", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, expected 400", rec.Code)
	}
	assertErrorBody(t, rec.Body.Bytes())
}

func TestHandleSetPolicy_MalformedBody(t *testing.T) {
	client := &mockClient{}
	srv := newTestServer(client)

	body := bytes.NewBufferString(`not json`)
	req := httptest.NewRequest(http.MethodPost, "/api/devices/a4:c3:65:12:34:8f/policy", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, expected 400", rec.Code)
	}
	assertErrorBody(t, rec.Body.Bytes())
}

func assertErrorBody(t *testing.T, body []byte) {
	t.Helper()
	var got map[string]string
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("parsing the error body: %v", err)
	}
	if got["error"] == "" {
		t.Errorf("error body has no error field: %s", body)
	}
}

// TestHandleSetPolicy_NormalizesMAC: the MAC from the URL is canonicalized
// both in the client call and in the response — differences in
// case/separators must not address a "different" host (spec §12).
func TestHandleSetPolicy_NormalizesMAC(t *testing.T) {
	for _, raw := range []string{"A4:C3:65:12:34:8F", "a4-c3-65-12-34-8f", "a4c3.6512.348f"} {
		client := &mockClient{}
		srv := newTestServer(client)

		body := bytes.NewBufferString(`{"policy_id":"Policy0"}`)
		req := httptest.NewRequest(http.MethodPost, "/api/devices/"+raw+"/policy", body)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s: code = %d, body: %s", raw, rec.Code, rec.Body.String())
		}
		if client.lastSetMAC != "a4:c3:65:12:34:8f" {
			t.Errorf("%s: client called with mac=%q", raw, client.lastSetMAC)
		}

		var got setPolicyResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: parsing the response: %v", raw, err)
		}
		if got.MAC != "a4:c3:65:12:34:8f" {
			t.Errorf("%s: response mac=%q, expected canonical", raw, got.MAC)
		}
	}
}

func TestHandleSetPolicy_InvalidMAC(t *testing.T) {
	client := &mockClient{}
	srv := newTestServer(client)

	body := bytes.NewBufferString(`{"policy_id":"Policy0"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/devices/not-a-mac/policy", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, expected 400", rec.Code)
	}
	assertErrorBody(t, rec.Body.Bytes())
	if client.lastSetMAC != "" {
		t.Errorf("an invalid MAC was sent to the router: %q", client.lastSetMAC)
	}
}
