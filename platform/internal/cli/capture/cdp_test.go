package capture

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// devtools answers /json/version like a browser, on loopback.
func devtools(t *testing.T, body string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/json/version" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(s.Close)
	return s
}

func TestResolveCDPTakesAWebSocketURL(t *testing.T) {
	const ws = "ws://127.0.0.1:9222/devtools/browser/8f3c"
	got, err := resolveCDP(" "+ws+" ", false)
	if err != nil || got != ws {
		t.Fatalf("resolveCDP = %q, %v", got, err)
	}
}

func TestResolveCDPReadsTheBrowsersWebSocket(t *testing.T) {
	// The endpoint's own host and port are kept: a browser behind a
	// tunnel or a published container port answers with its inside
	// address, and connecting there would fail.
	s := devtools(t, `{"webSocketDebuggerUrl":"ws://localhost:9222/devtools/browser/8f3c"}`)
	got, err := resolveCDP(s.URL, false)
	if err != nil {
		t.Fatal(err)
	}
	if want := "ws://" + strings.TrimPrefix(s.URL, "http://") + "/devtools/browser/8f3c"; got != want {
		t.Fatalf("resolveCDP = %q, want %q", got, want)
	}
}

func TestResolveCDPRefusesWhatItWontAttachTo(t *testing.T) {
	for _, tc := range []struct {
		name, endpoint, code, reason string
		allowRemote                  bool
	}{
		{name: "no scheme", endpoint: "127.0.0.1:9222", code: "invalid_cdp_endpoint", reason: "no scheme"},
		{name: "another scheme", endpoint: "file:///etc/passwd", code: "invalid_cdp_endpoint", reason: "must be ws, wss, http or https"},
		{name: "no host", endpoint: "ws:///devtools", code: "invalid_cdp_endpoint", reason: "no host"},
		// The SSRF guard: a name outside loopback is refused before any
		// request is made, so a CI variable can't aim capture at a
		// browser inside the network.
		{name: "a remote name", endpoint: "http://internal.example:9222", code: "invalid_cdp_endpoint", reason: "isn't a loopback address"},
		{name: "a remote address", endpoint: "ws://10.0.0.5:9222/devtools/browser/8f3c", code: "invalid_cdp_endpoint", reason: "isn't a loopback address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := resolveCDP(tc.endpoint, tc.allowRemote)
			ce, ok := err.(*CDPError)
			if !ok {
				t.Fatalf("err = %v, want a CDPError", err)
			}
			if ce.Code != tc.code || !strings.Contains(ce.Reason, tc.reason) {
				t.Fatalf("err = %+v, want %s containing %q", ce, tc.code, tc.reason)
			}
			if ce.Endpoint != strings.TrimSpace(tc.endpoint) {
				t.Errorf("endpoint = %q", ce.Endpoint)
			}
		})
	}
}

func TestResolveCDPAllowsARemoteHostOnlyWhenAsked(t *testing.T) {
	const ws = "wss://chrome.example:9222/devtools/browser/8f3c"
	if _, err := resolveCDP(ws, false); err == nil {
		t.Fatal("a remote host was attached to without --cdp-allow-remote")
	}
	got, err := resolveCDP(ws, true)
	if err != nil || got != ws {
		t.Fatalf("resolveCDP = %q, %v", got, err)
	}
}

func TestResolveCDPReportsAnEndpointItCantRead(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"not a version document", "<html>"},
		{"no websocket", `{"Browser":"Chrome/1"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := devtools(t, tc.body)
			_, err := resolveCDP(s.URL, false)
			ce, ok := err.(*CDPError)
			if !ok || ce.Code != "cdp_unreachable" {
				t.Fatalf("err = %v, want a cdp_unreachable CDPError", err)
			}
		})
	}
}
