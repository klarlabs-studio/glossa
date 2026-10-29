package app

import (
	"context"
	"errors"

	authgo "github.com/klarlabs-studio/auth-go/domain"

	"go.klarlabs.de/glossa/platform/internal/identity/domain"
)

// TOTPEnrollment is a pending authenticator secret.
type TOTPEnrollment struct {
	Secret     string
	OTPAuthURI string
}

// BeginTOTP creates (or replaces) a pending TOTP secret. It takes effect
// once ConfirmTOTP proves the person's app produces codes for it.
func (s *Service) BeginTOTP(ctx context.Context, person domain.PersonID) (TOTPEnrollment, error) {
	rec, err := s.Person(ctx, person)
	if err != nil {
		return TOTPEnrollment{}, err
	}
	if rec.TOTPEnabled {
		return TOTPEnrollment{}, ErrTOTPAlreadyEnabled
	}
	secret, err := authgo.NewTOTPSecret()
	if err != nil {
		return TOTPEnrollment{}, err
	}
	err = s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		return st.PendingTOTP(ctx, person, secret, s.now())
	})
	if err != nil {
		return TOTPEnrollment{}, err
	}
	return TOTPEnrollment{Secret: secret.String(), OTPAuthURI: s.totpCfg.ProvisioningURI(secret, rec.Email.String())}, nil
}

// ConfirmTOTP turns a pending secret on. The code is checked (and its
// time step consumed) by auth-go's replay-safe TOTPService.
func (s *Service) ConfirmTOTP(ctx context.Context, person domain.PersonID, code string) error {
	rec, err := s.totpRecord(ctx, person)
	if errors.Is(err, ErrNotFound) || err == nil && rec.Confirmed {
		return ErrTOTPNotPending
	}
	if err != nil {
		return err
	}
	if err := s.totp.Verify(ctx, userID(person), code); err != nil {
		return totpError(err)
	}
	return s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		return st.ConfirmTOTP(ctx, person, s.now())
	})
}

// DisableTOTP turns TOTP off after checking a current code.
func (s *Service) DisableTOTP(ctx context.Context, person domain.PersonID, code string) error {
	rec, err := s.totpRecord(ctx, person)
	if errors.Is(err, ErrNotFound) || err == nil && !rec.Confirmed {
		return ErrTOTPNotEnabled
	}
	if err != nil {
		return err
	}
	if err := s.totp.Verify(ctx, userID(person), code); err != nil {
		return totpError(err)
	}
	return s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		return st.DeleteTOTP(ctx, person)
	})
}

func (s *Service) totpRecord(ctx context.Context, person domain.PersonID) (TOTPRecord, error) {
	var rec TOTPRecord
	err := s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		rec, err = st.TOTP(ctx, person)
		return err
	})
	return rec, err
}

// Person loads a person.
func (s *Service) Person(ctx context.Context, id domain.PersonID) (PersonRecord, error) {
	var rec PersonRecord
	err := s.tx.InSystem(ctx, func(ctx context.Context, st SystemStore) error {
		var err error
		rec, err = st.PersonByID(ctx, id)
		return err
	})
	return rec, err
}
