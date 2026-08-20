package keenetic

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAuthenticate_UnexpectedStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	if err := client.Authenticate(t.Context()); err == nil {
		t.Fatal("expected an error on an unexpected GET /auth status")
	}
}

func TestAuthenticate_MissingChallengeHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	if err := client.Authenticate(t.Context()); err == nil {
		t.Fatal("expected an error on 401 without X-NDM-Realm/X-NDM-Challenge")
	}
}

func TestAuthenticate_PostAuthRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/auth":
			w.Header().Set("X-NDM-Realm", mockRealm)
			w.Header().Set("X-NDM-Challenge", "somechallenge")
			w.WriteHeader(http.StatusUnauthorized)
		case r.Method == http.MethodPost && r.URL.Path == "/auth":
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	if err := client.Authenticate(t.Context()); err == nil {
		t.Fatal("expected an error when the router rejects POST /auth")
	}
}

func TestGet_UnexpectedStatus(t *testing.T) {
	router := newMockRouter(t)
	mux := http.NewServeMux()
	mux.Handle("/auth", router)
	mux.HandleFunc("/rci/show/rc/ip/policy", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := newTestClient(t, srv)
	if _, err := client.ListPolicies(t.Context()); err == nil {
		t.Fatal("expected an error on an unexpected response status")
	}
}

func TestListDevices_MalformedJSON(t *testing.T) {
	mux := http.NewServeMux()
	router := newMockRouter(t)
	mux.Handle("/auth", router)
	mux.HandleFunc("/rci/show/ip/hotspot/host", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("session"); err != nil || c.Value == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("not json"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := newTestClient(t, srv)
	if err := client.Authenticate(t.Context()); err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if _, err := client.ListDevices(t.Context()); err == nil {
		t.Fatal("expected an error parsing malformed JSON")
	}
}

func TestSetPermit_Success(t *testing.T) {
	router := newMockRouter(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	client := newTestClient(t, srv)
	if err := client.SetPermit(t.Context(), "A4:C3:65:12:34:8F", false); err != nil {
		t.Fatalf("SetPermit: %v", err)
	}
}

func TestSetPermit_RouterError(t *testing.T) {
	router := newMockRouter(t)
	router.writeBody = readTestdata(t, "write-response-error.json")
	srv := httptest.NewServer(router)
	defer srv.Close()

	client := newTestClient(t, srv)
	if err := client.SetPermit(t.Context(), "a4:c3:65:12:34:8f", true); err == nil {
		t.Fatal("expected an error on status:error in the response")
	}
}

func TestCheckRCIErrors_MalformedBody(t *testing.T) {
	if err := checkRCIErrors([]byte("not json")); err == nil {
		t.Fatal("expected an error parsing a malformed response body")
	}
}

func TestNew_UsesTimeout(t *testing.T) {
	c, err := New("http://192.168.1.1", "admin", "pass", 3*time.Second)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if c.httpClient.Timeout != 3*time.Second {
		t.Errorf("timeout = %v, expected 3s", c.httpClient.Timeout)
	}
}

// TestNormalizeMAC checks MAC canonicalization per spec §12: differences in
// case or separators must not result in writing to a "different" address.
func TestNormalizeMAC(t *testing.T) {
	const want = "a4:c3:65:12:34:8f"
	for _, in := range []string{
		"a4:c3:65:12:34:8f",
		"A4:C3:65:12:34:8F",
		"a4-c3-65-12-34-8f",
		"A4-C3-65-12-34-8F",
		"a4c3.6512.348f",
		"  a4:c3:65:12:34:8f  ",
	} {
		if got := normalizeMAC(in); got != want {
			t.Errorf("normalizeMAC(%q) = %q, want %q", in, got, want)
		}
	}

	// Unparseable input isn't silently dropped, but isn't considered valid either.
	if got := normalizeMAC("NOT-A-MAC"); got != "not-a-mac" {
		t.Errorf("normalizeMAC on garbage = %q", got)
	}
	if validMAC("NOT-A-MAC") || validMAC("") || validMAC("a4:c3:65:12:34") {
		t.Error("validMAC accepted an invalid MAC")
	}
	if !validMAC("A4-C3-65-12-34-8F") {
		t.Error("validMAC rejected a valid dash-form MAC")
	}
}

// TestSetPolicy_NormalizesMACSeparators: a MAC written in dash form must
// reach the router in canonical colon form, otherwise it won't match the
// existing host (spec §12).
func TestSetPolicy_NormalizesMACSeparators(t *testing.T) {
	router := newMockRouter(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	client := newTestClient(t, srv)
	if err := client.SetPolicy(t.Context(), "A4-C3-65-12-34-8F", "Policy0"); err != nil {
		t.Fatalf("SetPolicy: %v", err)
	}

	body := string(router.lastWrite())
	if !strings.Contains(body, `"mac":"a4:c3:65:12:34:8f"`) {
		t.Errorf("a non-canonical MAC was sent to the router: %s", body)
	}
}

func TestSetPolicy_RejectsInvalidMAC(t *testing.T) {
	router := newMockRouter(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	client := newTestClient(t, srv)
	if err := client.SetPolicy(t.Context(), "not-a-mac", "Policy0"); err == nil {
		t.Error("SetPolicy with an invalid MAC returned nil")
	}
	if err := client.SetPermit(t.Context(), "not-a-mac", true); err == nil {
		t.Error("SetPermit with an invalid MAC returned nil")
	}
}

// TestAuthenticate_ConcurrentSingleLogin: concurrent requests hitting an
// expired session must not race each other into two separate logins.
func TestAuthenticate_Concurrent(t *testing.T) {
	router := newMockRouter(t)
	srv := httptest.NewServer(router)
	defer srv.Close()

	client := newTestClient(t, srv)

	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = client.ListDevices(t.Context())
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("parallel ListDevices #%d: %v", i, err)
		}
	}
	if got := router.authAttemptCount(); got != 1 {
		t.Errorf("expected exactly 1 login for 4 parallel requests, got %d", got)
	}
}
