// Package passkey configures auth-go's WebAuthn adapter
// (github.com/klarlabs-studio/auth-go/adapters/webauthn) as Identity's
// passkey authenticator and translates its errors into the application's.
package passkey

import (
	"context"
	"errors"
	"fmt"

	"github.com/klarlabs-studio/auth-go/adapters/webauthn"
	authgo "github.com/klarlabs-studio/auth-go/domain"

	"go.klarlabs.de/glossa/platform/internal/identity/app"
)

// Config is the WebAuthn relying party.
type Config struct {
	// RPID is the registrable domain passkeys are bound to, e.g. "glossa.dev".
	RPID string
	// RPName is shown by the authenticator.
	RPName string
	// Origins are the exact origins allowed to run ceremonies, e.g.
	// "https://app.glossa.dev".
	Origins []string
	// StateKey signs ceremony state (auth-go requires ≥ 32 bytes).
	StateKey []byte
}

// New returns the authenticator, reading credentials from repo.
func New(cfg Config, repo authgo.PasskeyRepository) (authgo.PasskeyAuthenticator, error) {
	a, err := webauthn.New(webauthn.Config{
		RPDisplayName: cfg.RPName,
		RPID:          cfg.RPID,
		RPOrigins:     cfg.Origins,
		StateKey:      cfg.StateKey,
	}, repo)
	if err != nil {
		return nil, fmt.Errorf("passkey: %w", err)
	}
	return authenticator{a}, nil
}

type authenticator struct{ *webauthn.Authenticator }

// BeginLogin reports a person without passkeys as app.ErrNoPasskeys.
func (a authenticator) BeginLogin(ctx context.Context, u authgo.UserID) (options, state []byte, err error) {
	options, state, err = a.Authenticator.BeginLogin(ctx, u)
	if errors.Is(err, webauthn.ErrNoPasskeys) {
		return nil, nil, app.ErrNoPasskeys
	}
	return options, state, err
}
