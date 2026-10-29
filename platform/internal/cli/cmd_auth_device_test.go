package cli

import (
	"strings"
	"testing"
	"time"
)

// `glossa login --device` (RFC 0006 §7.2): sign in as yourself by
// approving a code in Studio.

func TestLoginDeviceStoresTheSessionAndWaitsAsTold(t *testing.T) {
	srv := newFakeServer(t)
	srv.dev.script = []string{"authorization_pending", "slow_down", "authorization_pending"}
	w := newWorkspace(t).withProject(srv, nil)
	delete(w.env, "GLOSSA_TOKEN")

	var out loginJSON
	r := w.json(&out, "login", "--device", "--client-name", "build-01")
	r.want(t, ExitOK)

	if out.Method != "device" || out.Person == nil || out.Person.Email != "owner@example.com" || out.Person.ExpiresAt == nil {
		t.Errorf("login = %+v / %+v", out, out.Person)
	}
	if srv.dev.clientName != "build-01" {
		t.Errorf("client name sent = %q", srv.dev.clientName)
	}
	if w.store.tokens[srv.URL()] != fakeDeviceToken {
		t.Errorf("stored = %q, want the device session", w.store.tokens[srv.URL()])
	}
	// The code is shown on stderr, so stdout stays one JSON document,
	// and the secrets are in neither.
	if !strings.Contains(r.stderr, "BCDF-GHJK") || !strings.Contains(r.stderr, "code=BCDF-GHJK") {
		t.Errorf("stderr does not show the code and link:\n%s", r.stderr)
	}
	if strings.Contains(r.stdout+r.stderr, fakeDeviceToken) || strings.Contains(r.stdout+r.stderr, fakeDeviceCode) {
		t.Error("a secret was printed")
	}
	// Four polls at the server's 5 s, widened by 5 s after slow_down.
	want := []time.Duration{5 * time.Second, 5 * time.Second, 10 * time.Second, 10 * time.Second}
	if len(w.slept) != len(want) {
		t.Fatalf("waits = %v, want %v", w.slept, want)
	}
	for i := range want {
		if w.slept[i] != want[i] {
			t.Errorf("waits = %v, want %v", w.slept, want)
			break
		}
	}
}

func TestLoginDeviceDeniedAndExpired(t *testing.T) {
	for code, what := range map[string]string{"access_denied": "denied", "expired_token": "expired"} {
		srv := newFakeServer(t)
		srv.dev.script = []string{"authorization_pending", code}
		w := newWorkspace(t).withProject(srv, nil)
		var doc errorDoc
		w.json(&doc, "login", "--device").want(t, ExitNetwork)
		if doc.Error.Code != code || !strings.Contains(doc.Error.Message, what) {
			t.Errorf("%s: error = %+v", code, doc.Error)
		}
		if len(w.store.tokens) != 0 {
			t.Errorf("%s: stored %v", code, w.store.tokens)
		}
	}
}

func TestLoginDeviceRefusesTokenStdin(t *testing.T) {
	w := newWorkspace(t).withProject(newFakeServer(t), nil)
	w.run("login", "--device", "--token-stdin").want(t, ExitUsage)
}

func TestWhoamiShowsADeviceSession(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, nil)
	delete(w.env, "GLOSSA_TOKEN")
	w.run("login", "--device").want(t, ExitOK)

	var out whoamiJSON
	w.json(&out, "whoami").want(t, ExitOK)
	if out.Kind != "device_session" || out.Person == nil || out.Person.Email != "owner@example.com" {
		t.Errorf("whoami = %+v", out)
	}
	if strings.Contains(out.Token, "CCCCCCCC") {
		t.Errorf("whoami leaked the session: %q", out.Token)
	}
	if human := w.run("whoami"); !strings.Contains(human.stdout, "device session") || !strings.Contains(human.stdout, "Olive Owner") {
		t.Errorf("whoami reads:\n%s", human.stdout)
	}
}

func TestLogoutEndsADeviceSessionOnTheServer(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, nil)
	delete(w.env, "GLOSSA_TOKEN")
	w.run("login", "--device").want(t, ExitOK)

	var out map[string]any
	w.json(&out, "logout").want(t, ExitOK)
	if out["removed"] != true || out["revoked"] != true || srv.dev.signedOut != 1 || srv.dev.live {
		t.Errorf("logout = %v, signed out %d times", out, srv.dev.signedOut)
	}
	if len(w.store.tokens) != 0 {
		t.Errorf("still stored: %v", w.store.tokens)
	}
}

// A server that can't be reached must not trap the credential on disk.
func TestLogoutForgetsTheSessionEvenWhenTheServerCannotEndIt(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, nil)
	delete(w.env, "GLOSSA_TOKEN")
	w.run("login", "--device").want(t, ExitOK)
	srv.dev.failSignOut = true

	var out map[string]any
	w.json(&out, "logout").want(t, ExitOK)
	if out["removed"] != true || out["revoked"] != false || out["revoke_error"] == nil {
		t.Errorf("logout = %v", out)
	}
	if len(w.store.tokens) != 0 {
		t.Errorf("still stored: %v", w.store.tokens)
	}
}

func TestLogoutOfAnAPITokenDoesNotSignOut(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, nil)
	w.store.tokens[srv.URL()] = testToken
	var out map[string]any
	w.json(&out, "logout").want(t, ExitOK)
	if out["revoked"] != false || srv.dev.signedOut != 0 {
		t.Errorf("logout = %v", out)
	}
}

// The stored session is a credential like any other: the commands that
// follow authenticate with it. (--history against a real server is
// covered by the M5 exit test; an API token stays refused by the server.)
func TestTheDeviceSessionAuthenticatesLaterCommands(t *testing.T) {
	srv := newFakeServer(t)
	w := newWorkspace(t).withProject(srv, nil)
	delete(w.env, "GLOSSA_TOKEN")
	w.run("login", "--device").want(t, ExitOK)
	w.run("whoami").want(t, ExitOK)
	w.run("logout").want(t, ExitOK)
	// Ended: the fake server no longer accepts it, and nothing is stored.
	w.run("whoami").want(t, ExitNetwork)
}
