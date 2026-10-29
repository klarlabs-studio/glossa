package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"go.klarlabs.de/glossa/platform/internal/cli/credentials"
	"go.klarlabs.de/glossa/platform/internal/cli/remote"
)

// Device sign-in (RFC 0006 §7.2, RFC 8628): `glossa login --device`
// signs the person in as themselves. The credential it stores is their
// own session as a bearer (glossa_dev_…), so it can do what they can,
// including what no API token may (importing v0.3's history).

const devTokenPrefix = "glossa_dev_"

// slowDownStep is how much RFC 8628 §3.5 adds to the interval when the
// server says slow_down.
const slowDownStep = 5 * time.Second

func isDeviceSession(tok string) bool { return strings.HasPrefix(tok, devTokenPrefix) }

func credentialKind(tok string) string {
	switch {
	case isDeviceSession(tok):
		return "device_session"
	case strings.HasPrefix(tok, "glossa_ci_"):
		return "ci_token"
	case strings.HasPrefix(tok, "glossa_ctx_"):
		return "context_token"
	}
	return "api_token"
}

type personJSON struct {
	ID          string     `json:"id"`
	Email       string     `json:"email"`
	DisplayName string     `json:"display_name,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

func (p *personJSON) label() string {
	if p.DisplayName != "" {
		return p.DisplayName + " <" + p.Email + ">"
	}
	return p.Email
}

// personOf names the person behind a device session; nil if the server
// won't say (whoami still works from the tenants).
func (inv *invocation) personOf(ctx context.Context, server, tok string) *personJSON {
	c, err := newClientWithToken(inv, server, tok)
	if err != nil {
		return nil
	}
	me, err := c.Me(ctx)
	if err != nil {
		return nil
	}
	p := &personJSON{ID: me.Person.Id, Email: string(me.Person.Email)}
	if me.Person.DisplayName != nil {
		p.DisplayName = *me.Person.DisplayName
	}
	return p
}

func (inv *invocation) sleep(ctx context.Context, d time.Duration) error {
	if inv.env.Sleep != nil {
		return inv.env.Sleep(ctx, d)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

func defaultClientName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "glossa CLI"
	}
	return "glossa CLI on " + host
}

// loginWithDevice runs the grant: ask, show the code on stderr (stdout
// stays one document with --json), poll, store the session.
func (inv *invocation) loginWithDevice(ctx context.Context, server, clientName string) error {
	if clientName == "" {
		clientName = defaultClientName()
	}
	// Starting and polling carry no credential: the codes are.
	c, err := newClientWithToken(inv, server, "")
	if err != nil {
		return err
	}
	auth, err := c.StartDeviceAuthorization(ctx, clientName)
	if err != nil {
		return inv.apiError(err, "can't start a device sign-in")
	}
	fmt.Fprintf(inv.env.Stderr, "To sign in, open\n\n  %s\n\nand check that the code is\n\n  %s\n\nThe code expires in %d minutes. Waiting for you to approve it…\n",
		auth.VerificationUriComplete, auth.UserCode, (auth.ExpiresIn+59)/60)

	sess, err := inv.pollDevice(ctx, c, auth.DeviceCode, time.Duration(auth.Interval)*time.Second)
	if err != nil {
		return err
	}
	store, err := inv.store(server, sess.AccessToken)
	if err != nil {
		return err
	}
	out := loginJSON{Schema: "glossa.cli.login/v1", Server: server, StoredIn: store, Method: "device"}
	if person := inv.personOf(ctx, server, sess.AccessToken); person != nil {
		person.ExpiresAt = &sess.ExpiresAt
		out.Person = person
	} else {
		out.Person = &personJSON{ID: sess.PersonId.String(), ExpiresAt: &sess.ExpiresAt}
	}
	if t, err := inv.verifyToken(ctx, server, sess.AccessToken); err == nil {
		out.Tenant = t
	}
	return inv.emit(out, func(p *printer) {
		who := out.Person.label()
		if out.Person.Email == "" {
			who = out.Person.ID
		}
		p.line("%s Signed in to %s as %s", p.pass(), server, who)
		p.line("  session stored in %s, valid until %s", store, sess.ExpiresAt.Format(time.RFC3339))
		p.line("  `glossa logout` ends it")
	})
}

// pollDevice polls at the server's interval, widening it on slow_down,
// until the person decides or the code lapses.
func (inv *invocation) pollDevice(ctx context.Context, c *remote.Client, deviceCode string, interval time.Duration) (remote.DeviceSession, error) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		if err := inv.sleep(ctx, interval); err != nil {
			return remote.DeviceSession{}, err
		}
		sess, err := c.RedeemDeviceAuthorization(ctx, deviceCode)
		if err == nil {
			return sess, nil
		}
		var ae *remote.APIError
		if !errors.As(err, &ae) {
			return remote.DeviceSession{}, inv.apiError(err, "can't finish the device sign-in")
		}
		switch ae.Code {
		case "authorization_pending":
		case "slow_down":
			interval += slowDownStep
		case "access_denied":
			return remote.DeviceSession{}, &Error{Exit: ExitNetwork, Code: ae.Code, What: "the sign-in was denied",
				Why: "whoever opened the page chose Deny", Fix: "run `glossa login --device` again if that was a mistake"}
		case "expired_token":
			return remote.DeviceSession{}, &Error{Exit: ExitNetwork, Code: ae.Code, What: "the code expired before it was approved",
				Why: "codes last 15 minutes and work once", Fix: "run `glossa login --device` again"}
		default:
			return remote.DeviceSession{}, inv.apiError(err, "can't finish the device sign-in")
		}
	}
}

// revokeDeviceSession ends the stored credential on the server if it is
// a device session, so logout kills the session and not only the file.
func (inv *invocation) revokeDeviceSession(ctx context.Context, store credentials.Store, server string) (bool, error) {
	tok, err := store.Get(server)
	if err != nil || !isDeviceSession(tok) {
		return false, nil
	}
	c, err := newClientWithToken(inv, server, tok)
	if err != nil {
		return false, err
	}
	if err := c.SignOut(ctx); err != nil {
		var ae *remote.APIError
		if errors.As(err, &ae) && ae.Status == 401 {
			return false, nil // already ended
		}
		return false, err
	}
	return true, nil
}
