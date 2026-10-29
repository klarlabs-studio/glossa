package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
)

var t0 = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func TestUserCodesDrawFromTheAlphabetOnly(t *testing.T) {
	seen := map[rune]bool{}
	for range 500 {
		c, err := domain.NewUserCode()
		if err != nil {
			t.Fatal(err)
		}
		s := c.String()
		if len(s) != 9 || s[4] != '-' {
			t.Fatalf("user code %q is not XXXX-XXXX", s)
		}
		for _, r := range strings.ReplaceAll(s, "-", "") {
			if !strings.ContainsRune(domain.UserCodeAlphabet, r) {
				t.Fatalf("user code %q has %q, outside the alphabet", s, r)
			}
			seen[r] = true
		}
	}
	// 4,000 letters drawn: every one of the twenty should have come up.
	if len(seen) != len(domain.UserCodeAlphabet) {
		t.Errorf("only %d of %d letters were ever drawn", len(seen), len(domain.UserCodeAlphabet))
	}
	// No look-alikes and no vowels: nothing reads as a digit or a word.
	for _, r := range "AEIOUY0123456789" {
		if strings.ContainsRune(domain.UserCodeAlphabet, r) {
			t.Errorf("the alphabet holds %q", r)
		}
	}
}

func TestUserCodesNormalize(t *testing.T) {
	want, err := domain.ParseUserCode("BCDF-GHJK")
	if err != nil {
		t.Fatal(err)
	}
	for _, typed := range []string{"BCDFGHJK", "bcdf-ghjk", " bcdf ghjk ", "Bcdf-GhJk", "BC-DF-GH-JK"} {
		got, err := domain.ParseUserCode(typed)
		if err != nil {
			t.Errorf("%q: %v", typed, err)
			continue
		}
		if got != want || got.Hash() != want.Hash() || got.String() != "BCDF-GHJK" {
			t.Errorf("%q normalizes to %q, want BCDF-GHJK", typed, got)
		}
	}
	for _, bad := range []string{"", "BCDF-GHJ", "BCDF-GHJKL", "ABCD-EFGH", "BCDF-GHJ1", "BCDF_GHJK", "BCDF-GHJÄ"} {
		if _, err := domain.ParseUserCode(bad); !errors.Is(err, domain.ErrInvalidUserCode) {
			t.Errorf("%q: err = %v, want ErrInvalidUserCode", bad, err)
		}
	}
}

func TestNewDeviceAuthorizationStoresHashesOnly(t *testing.T) {
	d, dc, uc, err := domain.NewDeviceAuthorization("  glossa CLI on build-01 ", t0)
	if err != nil {
		t.Fatal(err)
	}
	if d.ClientName != "glossa CLI on build-01" || d.Status != domain.DevicePending ||
		d.Interval != 5*time.Second || !d.ExpiresAt.Equal(t0.Add(15*time.Minute)) {
		t.Errorf("authorization = %+v", d)
	}
	if d.DeviceCodeHash == dc.String() || d.UserCodeHash == uc.String() || len(d.DeviceCodeHash) != 64 ||
		d.DeviceCodeHash != dc.Hash() || d.UserCodeHash != uc.Hash() {
		t.Errorf("the codes are not stored as their hashes")
	}
	if p, err := domain.ParseDeviceCode(dc.String()); err != nil || p.Hash() != d.DeviceCodeHash {
		t.Errorf("the device code does not parse back: %v", err)
	}
	for _, name := range []string{"", "   ", strings.Repeat("x", 101), "tab\tname", "bell\a"} {
		if _, _, _, err := domain.NewDeviceAuthorization(name, t0); !errors.Is(err, domain.ErrInvalidClientName) {
			t.Errorf("client name %q: err = %v", name, err)
		}
	}
	if _, _, _, err := domain.NewDeviceAuthorization(strings.Repeat("ü", 100), t0); err != nil {
		t.Errorf("100 characters are refused: %v", err)
	}
}

func newAuthorization(t *testing.T) domain.DeviceAuthorization {
	t.Helper()
	d, _, _, err := domain.NewDeviceAuthorization("glossa CLI", t0)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

var person = domain.NewPersonID()

func TestPollingBeforeADecisionIsPendingAndTooFastSlowsDown(t *testing.T) {
	d := newAuthorization(t)
	if err := d.Poll(t0.Add(time.Second)); !errors.Is(err, domain.ErrAuthorizationPending) {
		t.Fatalf("first poll: %v", err)
	}
	// Within the interval: slow_down, and the interval grows by five
	// seconds for this poll and every later one (RFC 8628 §3.5).
	if err := d.Poll(t0.Add(3 * time.Second)); !errors.Is(err, domain.ErrSlowDown) {
		t.Fatalf("poll after 2 s: %v", err)
	}
	if d.Interval != 10*time.Second {
		t.Fatalf("interval = %v after slow_down", d.Interval)
	}
	// Five seconds is now too soon too.
	if err := d.Poll(t0.Add(8 * time.Second)); !errors.Is(err, domain.ErrSlowDown) {
		t.Fatalf("poll 5 s later: %v", err)
	}
	if err := d.Poll(t0.Add(18 * time.Second)); !errors.Is(err, domain.ErrSlowDown) || d.Interval != 20*time.Second {
		// 10 s after the last poll, but the interval is 15 s by now.
		t.Fatalf("poll 10 s later: %v, interval %v", err, d.Interval)
	}
	// Waiting the whole interval is fine, network jitter included.
	if err := d.Poll(t0.Add(38*time.Second - 300*time.Millisecond)); !errors.Is(err, domain.ErrAuthorizationPending) {
		t.Fatalf("poll after the interval: %v", err)
	}
}

func TestApprovedIsRedeemedOnce(t *testing.T) {
	d := newAuthorization(t)
	if err := d.Decide(person, true, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if d.Status != domain.DeviceApproved || d.Person != person || d.DecidedAt == nil {
		t.Fatalf("approved = %+v", d)
	}
	if err := d.Decide(person, false, t0.Add(time.Minute)); !errors.Is(err, domain.ErrDeviceNotPending) {
		t.Errorf("a second decision: %v", err)
	}
	now := t0.Add(2 * time.Minute)
	if err := d.Poll(now); err != nil {
		t.Fatalf("poll after approval: %v", err)
	}
	if err := d.Redeem(now); err != nil {
		t.Fatal(err)
	}
	later := now.Add(time.Minute)
	if err := d.Poll(later); !errors.Is(err, domain.ErrExpiredToken) {
		t.Errorf("poll after redemption: %v, want expired_token", err)
	}
	if err := d.Redeem(later); !errors.Is(err, domain.ErrExpiredToken) {
		t.Errorf("second redemption: %v", err)
	}
}

func TestDeniedStaysDenied(t *testing.T) {
	d := newAuthorization(t)
	if err := d.Decide(person, false, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if err := d.Poll(t0.Add(time.Duration(2+i) * time.Minute)); !errors.Is(err, domain.ErrAccessDenied) {
			t.Errorf("poll %d after denial: %v", i+1, err)
		}
	}
	if err := d.Redeem(t0.Add(5 * time.Minute)); !errors.Is(err, domain.ErrExpiredToken) {
		t.Errorf("redeeming a denied code: %v", err)
	}
}

func TestExpiry(t *testing.T) {
	d := newAuthorization(t)
	end := t0.Add(domain.DeviceAuthorizationTTL)
	if d.Pending(end) || !d.Pending(end.Add(-time.Second)) {
		t.Errorf("pending at the end = %v, a second before = %v", d.Pending(end), d.Pending(end.Add(-time.Second)))
	}
	if err := d.Decide(person, true, end); !errors.Is(err, domain.ErrDeviceNotPending) {
		t.Errorf("approving an expired code: %v", err)
	}
	if err := d.Poll(end); !errors.Is(err, domain.ErrExpiredToken) {
		t.Errorf("polling an expired code: %v", err)
	}
	// Approved in time but collected too late: the code has expired.
	a := newAuthorization(t)
	if err := a.Decide(person, true, end.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := a.Poll(end.Add(time.Second)); !errors.Is(err, domain.ErrExpiredToken) {
		t.Errorf("polling an approved, expired code: %v", err)
	}
}

func TestDeviceSessionBearer(t *testing.T) {
	raw := strings.Repeat("a", 43)
	b := domain.DeviceSessionBearer(raw)
	if !domain.IsDeviceSessionSecret(b) || b != "glossa_dev_"+raw {
		t.Fatalf("bearer = %q", b)
	}
	if got, ok := domain.DeviceSessionToken(b); !ok || got != raw {
		t.Errorf("token = %q, %v", got, ok)
	}
	for _, bad := range []string{raw, "glossa_api_" + raw, "glossa_dev_" + raw[:42], "glossa_dev_" + raw + "a", "glossa_dev_" + strings.Repeat("!", 43)} {
		if _, ok := domain.DeviceSessionToken(bad); ok {
			t.Errorf("%q is taken for a device session", bad)
		}
	}
	if domain.IsDeviceSessionSecret("glossa_api_" + raw) {
		t.Error("an API token claims to be a device session")
	}
}
