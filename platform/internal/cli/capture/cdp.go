package capture

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// CDPError is a DevTools endpoint glossa capture won't attach to
// (Code "invalid_cdp_endpoint") or can't reach ("cdp_unreachable").
type CDPError struct {
	Endpoint, Code, Reason string
}

func (e *CDPError) Error() string { return "can't attach to " + e.Endpoint + ": " + e.Reason }

// attachTimeout bounds reading a browser's DevTools version.
const attachTimeout = 10 * time.Second

// resolveCDP turns the endpoint of a browser someone else started into
// the browser WebSocket URL scout attaches to: ws:// and wss:// are used
// as they are; http:// and https:// are asked for their
// webSocketDebuggerUrl (/json/version), of which only the path is kept —
// the endpoint's own host and port stay, so a browser reached through a
// tunnel or a container's published port still answers.
//
// A CDP connection is full control of a browser: its pages, their
// cookies, and every address it can reach. The endpoint is the one thing
// capture connects to that isn't a page of the plan, and it arrives from
// a flag or an environment variable — a CI variable, say — so its host
// must be a loopback address. allowRemote (--cdp-allow-remote) lifts
// that for a browser on another host, and the operator owns the risk.
func resolveCDP(endpoint string, allowRemote bool) (string, error) {
	raw := strings.TrimSpace(endpoint)
	bad := func(format string, args ...any) (string, error) {
		return "", &CDPError{Endpoint: raw, Code: "invalid_cdp_endpoint", Reason: fmt.Sprintf(format, args...)}
	}
	if !strings.Contains(raw, "://") {
		// "127.0.0.1:9222" parses as a scheme, and url.Parse's own error
		// for it says nothing about what's missing.
		return bad("no scheme: write ws://host:port/devtools/browser/… or http://host:port")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return bad("%v", err)
	}
	switch u.Scheme {
	case "ws", "wss", "http", "https":
	default:
		return bad("the scheme must be ws, wss, http or https, not %q", u.Scheme)
	}
	if err := loopbackOnly(u, allowRemote); err != nil {
		return bad("%v", err)
	}
	if u.Scheme == "ws" || u.Scheme == "wss" {
		return u.String(), nil
	}
	path, err := browserWebSocketPath(u)
	if err != nil {
		return "", &CDPError{Endpoint: raw, Code: "cdp_unreachable", Reason: err.Error()}
	}
	ws := url.URL{Scheme: "ws", Host: u.Host, Path: path}
	if u.Scheme == "https" {
		ws.Scheme = "wss"
	}
	return ws.String(), nil
}

// loopbackOnly reports whether the endpoint's host may be attached to.
func loopbackOnly(u *url.URL, allowRemote bool) error {
	host := u.Hostname()
	switch {
	case host == "":
		return errors.New("no host")
	case allowRemote:
		return nil
	case strings.EqualFold(host, "localhost"):
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("%s isn't a loopback address, and a DevTools endpoint is full control of that browser", host)
}

// browserWebSocketPath reads /json/version and returns the path of the
// browser's webSocketDebuggerUrl.
func browserWebSocketPath(u *url.URL) (string, error) {
	v := url.URL{Scheme: u.Scheme, Host: u.Host, Path: strings.TrimSuffix(u.Path, "/") + "/json/version"}
	client := &http.Client{
		Timeout: attachTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("the DevTools endpoint redirected; a browser's doesn't")
		},
	}
	resp, err := client.Get(v.String())
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %s", v.String(), resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var info struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return "", fmt.Errorf("%s doesn't answer a DevTools version document: %w", v.String(), err)
	}
	ws, err := url.Parse(info.WebSocketDebuggerURL)
	if err != nil || (ws.Scheme != "ws" && ws.Scheme != "wss") || ws.Path == "" {
		return "", fmt.Errorf("%s names no browser WebSocket (webSocketDebuggerUrl %q)", v.String(), info.WebSocketDebuggerURL)
	}
	return ws.Path, nil
}
