// Package keenetic implements a client for the (unofficial) RCI HTTP API
// exposed by Keenetic routers: challenge-response authentication, reading
// registered hosts and connection policies, and writing a host's policy.
package keenetic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"time"
)

// Client talks RCI to a single Keenetic router over a cookie session.
type Client struct {
	baseURL    string
	login      string
	password   string
	httpClient *http.Client

	// authMu serializes logins. Without it, two requests that hit an
	// expired session at the same time (e.g. the frontend's parallel
	// /api/devices + /api/policies) would each run GET /auth, and the
	// second challenge would invalidate the first — one of the two logins
	// then fails spuriously. Held for the whole two-step exchange.
	authMu sync.Mutex
}

// New creates a Client for the router at baseURL (e.g. "http://192.168.1.1").
// It is not authenticated yet — call Authenticate, or just start issuing
// requests: ListDevices/ListPolicies/SetPolicy authenticate transparently
// on their own if the session is missing or expired.
func New(baseURL, login, password string, timeout time.Duration) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("keenetic: cookiejar: %w", err)
	}
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		login:    login,
		password: password,
		httpClient: &http.Client{
			Jar:     jar,
			Timeout: timeout,
		},
	}, nil
}

// Authenticate performs the two-step challenge-response login: GET /auth to
// obtain X-NDM-Realm/X-NDM-Challenge, then POST /auth with the computed
// hash. On success the session cookie is stored in the client's jar for
// subsequent requests.
func (c *Client) Authenticate(ctx context.Context) error {
	c.authMu.Lock()
	defer c.authMu.Unlock()
	return c.authenticateLocked(ctx)
}

// authenticateLocked performs the actual two-step exchange; callers must
// hold authMu.
func (c *Client) authenticateLocked(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/auth", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("keenetic: GET /auth: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode == http.StatusOK {
		// Already-authenticated session reporting 200 on /auth — nothing to do.
		return nil
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("keenetic: GET /auth: unexpected status %d", resp.StatusCode)
	}

	realm := resp.Header.Get("X-NDM-Realm")
	challenge := resp.Header.Get("X-NDM-Challenge")
	if realm == "" || challenge == "" {
		return fmt.Errorf("keenetic: GET /auth returned 401 without X-NDM-Realm/X-NDM-Challenge")
	}

	shaHex := computeAuthResponse(c.login, realm, c.password, challenge)

	body, err := json.Marshal(map[string]string{"login": c.login, "password": shaHex})
	if err != nil {
		return err
	}

	req, err = http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/auth", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp2, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("keenetic: POST /auth: %w", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	respBody, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != http.StatusOK {
		return fmt.Errorf("keenetic: POST /auth: status %d, body: %s", resp2.StatusCode, respBody)
	}
	return nil
}

// get issues an authenticated GET against the router, transparently logging
// in first if there is no session yet, and retrying once after a fresh
// login if the router responds 401 (session expired).
func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	body, status, err := c.rawGet(ctx, path)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized {
		if authErr := c.Authenticate(ctx); authErr != nil {
			return nil, fmt.Errorf("keenetic: re-authentication after 401: %w", authErr)
		}
		body, status, err = c.rawGet(ctx, path)
		if err != nil {
			return nil, err
		}
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("keenetic: GET %s: status %d", path, status)
	}
	return body, nil
}

func (c *Client) rawGet(ctx context.Context, path string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("keenetic: GET %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("keenetic: GET %s: reading body: %w", path, err)
	}
	return body, resp.StatusCode, nil
}

// postRCI POSTs a batch of RCI commands to /rci/, transparently re-logging
// in and retrying once on a 401. It returns the raw response body; the
// caller is responsible for inspecting it for per-command "status": "error"
// entries via parseRCIErrors, since the router answers those with HTTP 200.
func (c *Client) postRCI(ctx context.Context, commands interface{}) ([]byte, error) {
	payload, err := json.Marshal(commands)
	if err != nil {
		return nil, err
	}

	body, status, err := c.rawPostRCI(ctx, payload)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized {
		if authErr := c.Authenticate(ctx); authErr != nil {
			return nil, fmt.Errorf("keenetic: re-authentication after 401: %w", authErr)
		}
		body, status, err = c.rawPostRCI(ctx, payload)
		if err != nil {
			return nil, err
		}
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("keenetic: POST /rci/: status %d", status)
	}
	return body, nil
}

func (c *Client) rawPostRCI(ctx context.Context, payload []byte) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/rci/", bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("keenetic: POST /rci/: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("keenetic: POST /rci/: reading body: %w", err)
	}
	return body, resp.StatusCode, nil
}

// normalizeMAC canonicalizes a MAC address to lowercase colon-separated
// form ("a4:c3:65:12:34:8f"). The router's own responses already use that
// form, but MACs coming from our HTTP API (URL path segments) may differ in
// case *and* separator ("A4-C3-65-12-34-8F", "a4c3.6512.348f") — writing an
// unnormalized MAC to the router would silently fail to match the existing
// host, or create a bogus one (spec §12: "case, separators").
//
// Input that isn't a parseable MAC is returned lower-cased and trimmed
// rather than rejected here; callers that need validation use validMAC.
func normalizeMAC(mac string) string {
	trimmed := strings.TrimSpace(mac)
	if hw, err := net.ParseMAC(trimmed); err == nil && len(hw) == 6 {
		return hw.String()
	}
	return strings.ToLower(trimmed)
}

// validMAC reports whether mac is a well-formed 6-byte MAC address in any of
// the separator forms net.ParseMAC accepts.
func validMAC(mac string) bool {
	hw, err := net.ParseMAC(strings.TrimSpace(mac))
	return err == nil && len(hw) == 6
}
