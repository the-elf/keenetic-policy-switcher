package keenetic

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

const (
	mockLogin    = "admin"
	mockPassword = "test-password"
	mockRealm    = "NDM"
)

// mockRouter emulates just enough of a Keenetic router's RCI surface to
// exercise Client end to end, per spec §9.3: challenge-response auth, session
// cookies, fixture-backed show endpoints, and a batch write endpoint whose
// response can be told to look like success or a router-side error.
type mockRouter struct {
	mu sync.Mutex

	lastChallenge string
	sessionToken  string // "" means no session is currently valid
	authAttempts  int

	hostStatusBody []byte
	hostConfigBody []byte
	policyBody     []byte
	writeBody      []byte

	lastWriteRequest []byte
}

func newMockRouter(t *testing.T) *mockRouter {
	t.Helper()
	return &mockRouter{
		hostStatusBody: readTestdata(t, "hotspot-host.json"),
		hostConfigBody: readTestdata(t, "rc-ip-hotspot-host.json"),
		policyBody:     readTestdata(t, "rc-ip-policy.json"),
		writeBody:      readTestdata(t, "write-response-success.json"),
	}
}

func (m *mockRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch {
	case r.URL.Path == "/auth" && r.Method == http.MethodGet:
		m.handleAuthGet(w, r)
	case r.URL.Path == "/auth" && r.Method == http.MethodPost:
		m.handleAuthPost(w, r)
	case r.URL.Path == "/rci/show/ip/hotspot/host" && r.Method == http.MethodGet:
		m.handleShow(w, r, m.hostStatusBody)
	case r.URL.Path == "/rci/show/rc/ip/hotspot/host" && r.Method == http.MethodGet:
		m.handleShow(w, r, m.hostConfigBody)
	case r.URL.Path == "/rci/show/rc/ip/policy" && r.Method == http.MethodGet:
		m.handleShow(w, r, m.policyBody)
	case r.URL.Path == "/rci/" && r.Method == http.MethodPost:
		m.handleRCIPost(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (m *mockRouter) handleAuthGet(w http.ResponseWriter, r *http.Request) {
	if m.sessionValid(r) {
		w.WriteHeader(http.StatusOK)
		return
	}
	m.lastChallenge = randomHex(16)
	w.Header().Set("X-NDM-Realm", mockRealm)
	w.Header().Set("X-NDM-Challenge", m.lastChallenge)
	w.WriteHeader(http.StatusUnauthorized)
}

func (m *mockRouter) handleAuthPost(w http.ResponseWriter, r *http.Request) {
	var creds struct {
		Login    string `json:"login"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}

	want := computeAuthResponse(mockLogin, mockRealm, mockPassword, m.lastChallenge)
	if creds.Login != mockLogin || creds.Password != want {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	m.authAttempts++
	m.sessionToken = fmt.Sprintf("tok%d", m.authAttempts)
	http.SetCookie(w, &http.Cookie{Name: "session", Value: m.sessionToken, Path: "/"})
	w.WriteHeader(http.StatusOK)
}

func (m *mockRouter) handleShow(w http.ResponseWriter, r *http.Request, body []byte) {
	if !m.sessionValid(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (m *mockRouter) handleRCIPost(w http.ResponseWriter, r *http.Request) {
	if !m.sessionValid(r) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	body, err := readAll(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	m.lastWriteRequest = body
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(m.writeBody)
}

func (m *mockRouter) sessionValid(r *http.Request) bool {
	if m.sessionToken == "" {
		return false
	}
	c, err := r.Cookie("session")
	return err == nil && c.Value == m.sessionToken
}

// expireSession invalidates the current session token, simulating the
// router forgetting a session (timeout, reboot, admin logged in elsewhere):
// every subsequent request — including GET /auth — will 401 until the
// client logs in again.
func (m *mockRouter) expireSession() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionToken = ""
}

// lastWrite returns the raw body of the most recent POST /rci/.
func (m *mockRouter) lastWrite() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastWriteRequest
}

func (m *mockRouter) authAttemptCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.authAttempts
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func readAll(r *http.Request) ([]byte, error) {
	defer func() { _ = r.Body.Close() }()
	return io.ReadAll(r.Body)
}

func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	c, err := New(srv.URL, mockLogin, mockPassword, 5*time.Second)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// TestClient_EndToEnd walks the full scenario from spec §9.3: login → read
// devices and policies → change policy → save, all within one session
// (a single login for the whole sequence).
func TestClient_EndToEnd(t *testing.T) {
	router := newMockRouter(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	client := newTestClient(t, srv)

	devices, err := client.ListDevices(t.Context())
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devices) == 0 {
		t.Fatal("ListDevices returned an empty list")
	}

	policies, err := client.ListPolicies(t.Context())
	if err != nil {
		t.Fatalf("ListPolicies: %v", err)
	}
	if len(policies) == 0 {
		t.Fatal("ListPolicies returned an empty list")
	}

	if err := client.SetPolicy(t.Context(), "a4:c3:65:12:34:8f", "Policy2"); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}

	if got := router.authAttemptCount(); got != 1 {
		t.Errorf("expected 1 login attempt for the whole sequence, got %d", got)
	}

	var sentCommands []map[string]interface{}
	if err := json.Unmarshal(router.lastWriteRequest, &sentCommands); err != nil {
		t.Fatalf("parsing the sent batch: %v", err)
	}
	if len(sentCommands) != 2 {
		t.Errorf("expected a batch of 2 commands (policy + save), got %d", len(sentCommands))
	}
}

// TestClient_ReauthOn401 checks transparent re-authentication: an expired
// session answers 401 on a working request, the client logs in again and
// retries the request once, without surfacing an error.
func TestClient_ReauthOn401(t *testing.T) {
	router := newMockRouter(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	client := newTestClient(t, srv)

	if _, err := client.ListDevices(t.Context()); err != nil {
		t.Fatalf("first ListDevices: %v", err)
	}
	if got := router.authAttemptCount(); got != 1 {
		t.Fatalf("expected 1 login after the first call, got %d", got)
	}

	router.expireSession()

	devices, err := client.ListDevices(t.Context())
	if err != nil {
		t.Fatalf("ListDevices after an expired session returned an error instead of re-authenticating: %v", err)
	}
	if len(devices) == 0 {
		t.Fatal("ListDevices after re-authentication returned an empty list")
	}
	if got := router.authAttemptCount(); got != 2 {
		t.Errorf("expected 2 logins (re-authentication after 401), got %d", got)
	}
}

// TestClient_SetPolicy_RouterError checks that status:error in the response
// body (under HTTP 200) is recognized as an error, not a silent success.
func TestClient_SetPolicy_RouterError(t *testing.T) {
	router := newMockRouter(t)
	router.writeBody = readTestdata(t, "write-response-error.json")
	srv := httptest.NewServer(router)
	defer srv.Close()

	client := newTestClient(t, srv)

	err := client.SetPolicy(t.Context(), "a4:c3:65:12:34:8f", "PolicyNope")
	if err == nil {
		t.Fatal("SetPolicy on a status:error response returned nil, expected an error")
	}
}
