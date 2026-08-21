// Package api implements the HTTP JSON API consumed by the embedded
// frontend: listing devices/policies and changing a device's policy. It
// talks to the router only through the KeeneticClient interface, so it can
// be tested with a mock — no real router needed (see spec §9.2).
package api

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"strings"
)

// Device and Policy are the shapes this package needs from
// internal/keenetic, duplicated here (rather than importing the keenetic
// package's types directly) so KeeneticClient stays a narrow interface that
// is trivial to mock in tests.
type Device struct {
	MAC      string
	Name     string
	IP       string
	Online   bool
	PolicyID string
}

type Policy struct {
	ID   string
	Name string
}

// KeeneticClient is the subset of *keenetic.Client the HTTP handlers need.
type KeeneticClient interface {
	ListDevices(ctx context.Context) ([]Device, error)
	ListPolicies(ctx context.Context) ([]Policy, error)
	SetPolicy(ctx context.Context, mac, policyID string) error
}

// FavoritesStore is the subset of *favorites.Store the HTTP handlers need.
// Favorites are local server state (no router round-trip involved), so
// these methods don't take a context.
type FavoritesStore interface {
	Contains(mac string) bool
	Add(mac string) error
	Remove(mac string) error
}

// Handler holds the dependencies for the /api/* routes.
type Handler struct {
	client    KeeneticClient
	favorites FavoritesStore
	logger    *log.Logger
}

// NewHandler builds a Handler backed by client and favorites. If logger is
// nil, log.Default() is used.
func NewHandler(client KeeneticClient, favorites FavoritesStore, logger *log.Logger) *Handler {
	if logger == nil {
		logger = log.Default()
	}
	return &Handler{client: client, favorites: favorites, logger: logger}
}

// Register wires the /api/* routes onto mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/devices", h.handleListDevices)
	mux.HandleFunc("GET /api/policies", h.handleListPolicies)
	mux.HandleFunc("POST /api/devices/{mac}/policy", h.handleSetPolicy)
	mux.HandleFunc("POST /api/devices/{mac}/favorite", h.handleSetFavorite)
}

type devicesResponse struct {
	RouterOnline bool        `json:"router_online"`
	Devices      []deviceDTO `json:"devices"`
}

type deviceDTO struct {
	MAC      string `json:"mac"`
	Name     string `json:"name"`
	IP       string `json:"ip"`
	Online   bool   `json:"online"`
	PolicyID string `json:"policy_id"`
	Favorite bool   `json:"favorite"`
}

// handleListDevices serves GET /api/devices. Per spec §7.5, a router that is
// unreachable is not an HTTP error for this endpoint: the frontend needs
// router_online:false to show its "router not responding" banner without
// the device list request itself failing.
func (h *Handler) handleListDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := h.client.ListDevices(r.Context())
	if err != nil {
		h.logger.Printf("ListDevices: %v", err)
		writeJSON(w, http.StatusOK, devicesResponse{RouterOnline: false, Devices: []deviceDTO{}})
		return
	}

	dtos := make([]deviceDTO, 0, len(devices))
	for _, d := range devices {
		dtos = append(dtos, deviceDTO{
			MAC:      d.MAC,
			Name:     d.Name,
			IP:       d.IP,
			Online:   d.Online,
			PolicyID: d.PolicyID,
			Favorite: h.favorites.Contains(d.MAC),
		})
	}
	writeJSON(w, http.StatusOK, devicesResponse{RouterOnline: true, Devices: dtos})
}

type policiesResponse struct {
	Policies []policyDTO `json:"policies"`
}

type policyDTO struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (h *Handler) handleListPolicies(w http.ResponseWriter, r *http.Request) {
	policies, err := h.client.ListPolicies(r.Context())
	if err != nil {
		h.logger.Printf("ListPolicies: %v", err)
		writeError(w, http.StatusBadGateway, "router did not respond to the policy list request")
		return
	}

	dtos := make([]policyDTO, 0, len(policies))
	for _, p := range policies {
		dtos = append(dtos, policyDTO(p))
	}
	writeJSON(w, http.StatusOK, policiesResponse{Policies: dtos})
}

type setPolicyRequest struct {
	PolicyID string `json:"policy_id"`
}

type setPolicyResponse struct {
	OK       bool   `json:"ok"`
	MAC      string `json:"mac"`
	PolicyID string `json:"policy_id"`
}

func (h *Handler) handleSetPolicy(w http.ResponseWriter, r *http.Request) {
	mac, ok := normalizeMAC(r.PathValue("mac"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid device mac")
		return
	}

	var req setPolicyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.PolicyID == "" {
		writeError(w, http.StatusBadRequest, "policy_id is required")
		return
	}

	if err := h.client.SetPolicy(r.Context(), mac, req.PolicyID); err != nil {
		h.logger.Printf("SetPolicy(%s, %s): %v", mac, req.PolicyID, err)
		writeError(w, http.StatusBadGateway, "failed to apply the policy on the router")
		return
	}

	writeJSON(w, http.StatusOK, setPolicyResponse{OK: true, MAC: mac, PolicyID: req.PolicyID})
}

type setFavoriteRequest struct {
	// A pointer so a missing "favorite" key (nil) can be rejected instead of
	// silently defaulting to false and un-favoriting the device — the same
	// ambiguity handleSetPolicy avoids by requiring a non-empty policy_id.
	Favorite *bool `json:"favorite"`
}

type setFavoriteResponse struct {
	OK       bool   `json:"ok"`
	MAC      string `json:"mac"`
	Favorite bool   `json:"favorite"`
}

// handleSetFavorite serves POST /api/devices/{mac}/favorite. Unlike the
// policy handler this never touches the router — it's purely local state —
// so a store failure is a 500 (our disk), not a 502 (the router).
func (h *Handler) handleSetFavorite(w http.ResponseWriter, r *http.Request) {
	mac, ok := normalizeMAC(r.PathValue("mac"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid device mac")
		return
	}

	var req setFavoriteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Favorite == nil {
		writeError(w, http.StatusBadRequest, "favorite is required")
		return
	}

	var err error
	if *req.Favorite {
		err = h.favorites.Add(mac)
	} else {
		err = h.favorites.Remove(mac)
	}
	if err != nil {
		h.logger.Printf("SetFavorite(%s, %v): %v", mac, *req.Favorite, err)
		writeError(w, http.StatusInternalServerError, "failed to save favorites")
		return
	}

	writeJSON(w, http.StatusOK, setFavoriteResponse{OK: true, MAC: mac, Favorite: *req.Favorite})
}

// normalizeMAC validates the {mac} path segment and canonicalizes it to
// lowercase colon-separated form. Both the router write and the echoed
// response use the canonical form: a MAC that differs only in case or
// separator must address the same host (spec §12), and unparseable input is
// rejected here rather than forwarded to the router.
func normalizeMAC(raw string) (string, bool) {
	hw, err := net.ParseMAC(strings.TrimSpace(raw))
	if err != nil || len(hw) != 6 {
		return "", false
	}
	return hw.String(), true
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
