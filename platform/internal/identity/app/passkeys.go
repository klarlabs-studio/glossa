package app

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	authgo "github.com/klarlabs-studio/auth-go/domain"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

// CeremonyTTL bounds a passkey ceremony; it matches auth-go's WebAuthn
// timeout.
const CeremonyTTL = 5 * time.Minute

const (
	ceremonySignIn       = "sign_in"
	ceremonyRegistration = "registration"
)

// PasskeysEnabled reports whether a WebAuthn relying party is configured.
func (s *Service) PasskeysEnabled() bool { return s.passkeys != nil }

// PasskeyChallenge is a started ceremony: options for the browser and the
// key the browser must present with its response (in an HttpOnly cookie).
type PasskeyChallenge struct {
	Options []byte
	Key     string
}

// BeginPasskeySignIn starts an assertion for the account's passkeys.
func (s *Service) BeginPasskeySignIn(ctx context.Context, email string) (PasskeyChallenge, error) {
	if s.passkeys == nil {
		return PasskeyChallenge{}, ErrPasskeysDisabled
	}
	e, err := parseEmail(email)
	if err != nil {
		return PasskeyChallenge{}, err
	}
	var rec PersonRecord
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		rec, err = st.PersonByEmail(ctx, e)
		return err
	})
	if errors.Is(err, ErrNotFound) {
		return PasskeyChallenge{}, ErrNoPasskeys
	}
	if err != nil {
		return PasskeyChallenge{}, err
	}
	options, state, err := s.passkeys.BeginLogin(ctx, userID(rec.ID))
	if errors.Is(err, ErrNoPasskeys) { // the adapter translates auth-go's webauthn.ErrNoPasskeys
		return PasskeyChallenge{}, ErrNoPasskeys
	}
	if err != nil {
		return PasskeyChallenge{}, fmt.Errorf("identity: begin passkey sign-in: %w", err)
	}
	return s.saveCeremony(ctx, ceremonySignIn, domain.PersonID{}, options, state)
}

// FinishPasskeySignIn verifies the assertion and signs the person in.
func (s *Service) FinishPasskeySignIn(ctx context.Context, key string, response []byte) (SignedIn, error) {
	if s.passkeys == nil {
		return SignedIn{}, ErrPasskeysDisabled
	}
	c, err := s.takeCeremony(ctx, ceremonySignIn, key)
	if err != nil {
		return SignedIn{}, err
	}
	credID, err := s.passkeys.FinishLogin(ctx, c.State, response)
	if err != nil {
		s.logger.InfoContext(ctx, "passkey sign-in rejected", "error", err)
		return SignedIn{}, ErrPasskeyInvalid
	}
	var rec PersonRecord
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		person, err := st.PersonOfPasskey(ctx, credID)
		if err != nil {
			return err
		}
		rec, err = st.PersonByID(ctx, person)
		return err
	})
	if errors.Is(err, ErrNotFound) {
		return SignedIn{}, ErrPasskeyInvalid
	}
	if err != nil {
		return SignedIn{}, err
	}
	return s.completeSignIn(ctx, rec, MethodPasskey)
}

// BeginPasskeyRegistration starts enrolling a passkey for a person.
func (s *Service) BeginPasskeyRegistration(ctx context.Context, person domain.PersonID) (PasskeyChallenge, error) {
	if s.passkeys == nil {
		return PasskeyChallenge{}, ErrPasskeysDisabled
	}
	rec, err := s.Person(ctx, person)
	if err != nil {
		return PasskeyChallenge{}, err
	}
	options, state, err := s.passkeys.BeginRegistration(ctx, userID(person), rec.Email.String())
	if err != nil {
		return PasskeyChallenge{}, fmt.Errorf("identity: begin passkey registration: %w", err)
	}
	return s.saveCeremony(ctx, ceremonyRegistration, person, options, state)
}

// Passkey is a registered passkey.
type Passkey struct {
	ID        []byte
	Name      string
	CreatedAt time.Time
	// LastUsedAt is the last sign-in with it, nil before the first.
	LastUsedAt *time.Time
}

// PasskeyCursor is the keyset position after a passkey in the oldest
// first listing.
type PasskeyCursor struct {
	CreatedAt time.Time
	ID        []byte
}

// String encodes the cursor as a page's sort key.
func (c PasskeyCursor) String() string {
	return strconv.FormatInt(c.CreatedAt.UnixMicro(), 10) + "." + base64.RawURLEncoding.EncodeToString(c.ID)
}

// parsePasskeyCursor reads a page's sort key; "" is the first page.
func parsePasskeyCursor(s string) (PasskeyCursor, error) {
	if s == "" {
		return PasskeyCursor{CreatedAt: time.Unix(0, 0).UTC(), ID: []byte{}}, nil
	}
	micros, id, ok := strings.Cut(s, ".")
	n, err := strconv.ParseInt(micros, 10, 64)
	raw, err2 := base64.RawURLEncoding.DecodeString(id)
	if !ok || err != nil || err2 != nil || len(raw) == 0 {
		return PasskeyCursor{}, problem.New(http.StatusBadRequest, "invalid_page_token", "page_token is not one this API issued")
	}
	return PasskeyCursor{CreatedAt: time.UnixMicro(n).UTC(), ID: raw}, nil
}

// ListPasskeys lists a person's passkeys, oldest first, whatever device
// registered them. It works while passkeys are not configured, so
// people can still see (and remove) what they enrolled.
func (s *Service) ListPasskeys(ctx context.Context, person domain.PersonID, page pagination.Page) ([]Passkey, *string, error) {
	after, err := parsePasskeyCursor(page.After)
	if err != nil {
		return nil, nil, err
	}
	var rows []Passkey
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		rows, err = st.PasskeysOf(ctx, person, after, page.Limit())
		return err
	})
	if err != nil {
		return nil, nil, err
	}
	items, next := pagination.Trim(rows, page, func(p Passkey) string {
		return PasskeyCursor{CreatedAt: p.CreatedAt, ID: p.ID}.String()
	})
	return items, next, nil
}

// DeletePasskey removes one of the person's passkeys. Another person's
// passkey is ErrNotFound, like an unknown one.
func (s *Service) DeletePasskey(ctx context.Context, person domain.PersonID, id []byte) error {
	return s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		return st.DeletePasskeyOf(ctx, person, id)
	})
}

// FinishPasskeyRegistration verifies the attestation and stores the
// passkey under name.
func (s *Service) FinishPasskeyRegistration(ctx context.Context, person domain.PersonID, key string, response []byte, name string) (Passkey, error) {
	if s.passkeys == nil {
		return Passkey{}, ErrPasskeysDisabled
	}
	c, err := s.takeCeremony(ctx, ceremonyRegistration, key)
	if err != nil {
		return Passkey{}, err
	}
	if c.Person != person { // started by someone else's session
		return Passkey{}, ErrPasskeyInvalid
	}
	cred, err := s.passkeys.FinishRegistration(ctx, c.State, response)
	if err != nil {
		s.logger.InfoContext(ctx, "passkey registration rejected", "error", err)
		return Passkey{}, ErrPasskeyInvalid
	}
	if owner, err := personOf(cred.UserID); err != nil || owner != person {
		return Passkey{}, ErrPasskeyInvalid
	}
	cred.Name = name
	now := s.now()
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		return st.AddPasskey(ctx, person, cred, now)
	})
	if err != nil {
		return Passkey{}, err
	}
	return Passkey{ID: cred.ID, Name: name, CreatedAt: now}, nil
}

func (s *Service) saveCeremony(ctx context.Context, purpose string, person domain.PersonID, options, state []byte) (PasskeyChallenge, error) {
	key, err := authgo.NewToken()
	if err != nil {
		return PasskeyChallenge{}, err
	}
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		return st.SaveCeremony(ctx, Ceremony{
			KeyHash: authgo.HashToken(key), Purpose: purpose, Person: person,
			State: state, ExpiresAt: s.now().Add(CeremonyTTL),
		})
	})
	if err != nil {
		return PasskeyChallenge{}, err
	}
	return PasskeyChallenge{Options: options, Key: key.String()}, nil
}

// takeCeremony spends a ceremony: it can be answered once.
func (s *Service) takeCeremony(ctx context.Context, purpose, key string) (Ceremony, error) {
	raw, err := authgo.TokenFromString(key)
	if err != nil {
		return Ceremony{}, ErrPasskeyInvalid
	}
	var c Ceremony
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		c, err = st.TakeCeremony(ctx, authgo.HashToken(raw), purpose)
		return err
	})
	if errors.Is(err, ErrNotFound) || err == nil && !s.now().Before(c.ExpiresAt) {
		return Ceremony{}, ErrPasskeyInvalid
	}
	return c, err
}
