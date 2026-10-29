package cli

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// fakeDevice is the server's half of RFC 8628 as RFC 0006 §7.2 shapes
// it: a start, a poll that answers authorization_pending (then whatever
// script says), and the session the approved device holds.
type fakeDevice struct {
	mu sync.Mutex
	// script is what the next polls answer, one per poll, before the
	// session is handed out: "authorization_pending", "slow_down",
	// "access_denied" or "expired_token". When empty the poll succeeds.
	script []string
	// polls counts redemptions attempted; clientName is the last start's.
	polls      int
	clientName string
	// live is whether the session is valid; signedOut how often
	// DELETE /v1/auth/session ended it.
	live      bool
	signedOut int
	// stateless stops the sign-out from working (the server is gone).
	failSignOut bool
}

const (
	fakeDeviceCode  = "device-code-secret"
	fakeDeviceToken = "glossa_dev_CCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC"
)

func (d *fakeDevice) accepts(header string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.live && header == "Bearer "+fakeDeviceToken
}

func (f *fakeServer) routeDevice(mux *http.ServeMux) {
	d := f.dev
	mux.HandleFunc("POST /v1/auth/device-authorizations", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ClientName string `json:"client_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ClientName == "" {
			problemResp(w, 400, "invalid_request", "client_name is required")
			return
		}
		d.mu.Lock()
		d.clientName = body.ClientName
		d.mu.Unlock()
		writeJSONResp(w, 200, map[string]any{
			"device_code": fakeDeviceCode, "user_code": "BCDF-GHJK", "expires_in": 900, "interval": 5,
			"verification_uri":          "https://studio.example/device",
			"verification_uri_complete": "https://studio.example/device?code=BCDF-GHJK",
		})
	})
	mux.HandleFunc("POST /v1/auth/device-sessions", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			DeviceCode string `json:"device_code"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		d.mu.Lock()
		defer d.mu.Unlock()
		d.polls++
		if body.DeviceCode != fakeDeviceCode {
			problemResp(w, 400, "expired_token", "unknown code")
			return
		}
		if len(d.script) > 0 {
			code := d.script[0]
			d.script = d.script[1:]
			problemResp(w, 400, code, code)
			return
		}
		d.live = true
		writeJSONResp(w, 200, map[string]any{
			"access_token": fakeDeviceToken, "token_type": "Bearer",
			"expires_at": time.Now().UTC().Add(30 * 24 * time.Hour).Format(time.RFC3339),
			"person_id":  "00000000-0000-0000-0000-000000000042",
		})
	})
	mux.HandleFunc("GET /v1/me", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONResp(w, 200, map[string]any{
			"csrf_token": "", "memberships": []any{},
			"person": map[string]any{"id": "00000000-0000-0000-0000-000000000042", "email": "owner@example.com",
				"display_name": "Olive Owner", "email_verified": true, "totp_enabled": false,
				"individual_tenant_id": "ten_1", "created_at": "2026-09-19T00:00:00Z"},
		})
	})
	mux.HandleFunc("DELETE /v1/auth/session", func(w http.ResponseWriter, _ *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		if d.failSignOut {
			problemResp(w, 500, "internal", "boom")
			return
		}
		d.live = false
		d.signedOut++
		w.WriteHeader(204)
	})
}
