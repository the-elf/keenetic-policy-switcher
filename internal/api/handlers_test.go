package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
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

// mockFavorites is a hand-rolled FavoritesStore double, mirroring mockClient
// above: an in-memory set plus optional injected errors, so favorite-handler
// tests stay decoupled from the real internal/favorites package.
type mockFavorites struct {
	mu        sync.Mutex
	macs      map[string]bool
	addErr    error
	removeErr error
}

func newMockFavorites(initial ...string) *mockFavorites {
	m := &mockFavorites{macs: map[string]bool{}}
	for _, mac := range initial {
		m.macs[mac] = true
	}
	return m
}

func (m *mockFavorites) Contains(mac string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.macs[mac]
}

func (m *mockFavorites) Add(mac string) error {
	if m.addErr != nil {
		return m.addErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.macs[mac] = true
	return nil
}

func (m *mockFavorites) Remove(mac string) error {
	if m.removeErr != nil {
		return m.removeErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.macs, mac)
	return nil
}

func newTestServer(client KeeneticClient, favorites FavoritesStore) http.Handler {
	mux := http.NewServeMux()
	NewHandler(client, favorites, nil).Register(mux)
	return mux
}

func TestHandleListDevices_Success(t *testing.T) {
	client := &mockClient{devices: []Device{
		{MAC: "a4:c3:65:12:34:8f", Name: "Laptop", IP: "192.168.1.42", Online: true, PolicyID: "Policy0"},
	}}
	srv := newTestServer(client, newMockFavorites())

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
	if got.Devices[0].Favorite {
		t.Error("device not in the favorites store came back favorite=true")
	}
}

func TestHandleListDevices_MarksFavorite(t *testing.T) {
	client := &mockClient{devices: []Device{
		{MAC: "a4:c3:65:12:34:8f", Name: "Laptop", IP: "192.168.1.42", Online: true, PolicyID: "Policy0"},
		{MAC: "b5:d4:76:23:45:9a", Name: "Phone", IP: "192.168.1.43", Online: true, PolicyID: "Policy0"},
	}}
	srv := newTestServer(client, newMockFavorites("a4:c3:65:12:34:8f"))

	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var got devicesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("parsing the response: %v", err)
	}
	byMAC := map[string]bool{}
	for _, d := range got.Devices {
		byMAC[d.MAC] = d.Favorite
	}
	if !byMAC["a4:c3:65:12:34:8f"] {
		t.Error("expected a4:c3:65:12:34:8f to come back favorite=true")
	}
	if byMAC["b5:d4:76:23:45:9a"] {
		t.Error("expected b5:d4:76:23:45:9a to come back favorite=false")
	}
}

func TestHandleListDevices_RouterOffline(t *testing.T) {
	client := &mockClient{devicesErr: errors.New("dial tcp: connection refused")}
	srv := newTestServer(client, newMockFavorites())

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
	srv := newTestServer(client, newMockFavorites())

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
	srv := newTestServer(client, newMockFavorites())

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
	srv := newTestServer(client, newMockFavorites())

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
	srv := newTestServer(client, newMockFavorites())

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
	srv := newTestServer(client, newMockFavorites())

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
	srv := newTestServer(client, newMockFavorites())

	body := bytes.NewBufferString(`not json`)
	req := httptest.NewRequest(http.MethodPost, "/api/devices/a4:c3:65:12:34:8f/policy", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, expected 400", rec.Code)
	}
	assertErrorBody(t, rec.Body.Bytes())
}

func TestHandleSetFavorite_Add(t *testing.T) {
	client := &mockClient{}
	favorites := newMockFavorites()
	srv := newTestServer(client, favorites)

	body := bytes.NewBufferString(`{"favorite":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/devices/a4:c3:65:12:34:8f/favorite", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, expected 200, body: %s", rec.Code, rec.Body.String())
	}
	var got setFavoriteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("parsing the response: %v", err)
	}
	if !got.OK || got.MAC != "a4:c3:65:12:34:8f" || !got.Favorite {
		t.Errorf("unexpected response: %+v", got)
	}
	if !favorites.Contains("a4:c3:65:12:34:8f") {
		t.Error("store was not updated")
	}
}

func TestHandleSetFavorite_Remove(t *testing.T) {
	client := &mockClient{}
	favorites := newMockFavorites("a4:c3:65:12:34:8f")
	srv := newTestServer(client, favorites)

	body := bytes.NewBufferString(`{"favorite":false}`)
	req := httptest.NewRequest(http.MethodPost, "/api/devices/a4:c3:65:12:34:8f/favorite", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, expected 200, body: %s", rec.Code, rec.Body.String())
	}
	var got setFavoriteResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("parsing the response: %v", err)
	}
	if !got.OK || got.Favorite {
		t.Errorf("unexpected response: %+v", got)
	}
	if favorites.Contains("a4:c3:65:12:34:8f") {
		t.Error("store still has the mac after removal")
	}
}

func TestHandleSetFavorite_StoreError(t *testing.T) {
	client := &mockClient{}
	favorites := newMockFavorites()
	favorites.addErr = errors.New("disk full")
	srv := newTestServer(client, favorites)

	body := bytes.NewBufferString(`{"favorite":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/devices/a4:c3:65:12:34:8f/favorite", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("code = %d, expected 500", rec.Code)
	}
	assertErrorBody(t, rec.Body.Bytes())
}

func TestHandleSetFavorite_InvalidMAC(t *testing.T) {
	srv := newTestServer(&mockClient{}, newMockFavorites())

	body := bytes.NewBufferString(`{"favorite":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/devices/not-a-mac/favorite", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, expected 400", rec.Code)
	}
	assertErrorBody(t, rec.Body.Bytes())
}

func TestHandleSetFavorite_MalformedBody(t *testing.T) {
	srv := newTestServer(&mockClient{}, newMockFavorites())

	body := bytes.NewBufferString(`not json`)
	req := httptest.NewRequest(http.MethodPost, "/api/devices/a4:c3:65:12:34:8f/favorite", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, expected 400", rec.Code)
	}
	assertErrorBody(t, rec.Body.Bytes())
}

func TestHandleSetFavorite_MissingFavorite(t *testing.T) {
	client := &mockClient{}
	favorites := newMockFavorites()
	srv := newTestServer(client, favorites)

	body := bytes.NewBufferString(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/api/devices/a4:c3:65:12:34:8f/favorite", body)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, expected 400", rec.Code)
	}
	assertErrorBody(t, rec.Body.Bytes())
	if favorites.Contains("a4:c3:65:12:34:8f") {
		t.Error("a request missing \"favorite\" should not have favorited the device")
	}
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
		srv := newTestServer(client, newMockFavorites())

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
	srv := newTestServer(client, newMockFavorites())

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
