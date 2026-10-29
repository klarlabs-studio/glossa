package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	authgo "github.com/klarlabs-studio/auth-go/domain"

	"go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres/identitysql"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

// systemStore implements app.SystemStore on a system-scope transaction.
type systemStore struct {
	q      *identitysql.Queries
	cipher authgo.SecretCipher
}

var _ app.SystemStore = (*systemStore)(nil)

func (s *systemStore) CreatePerson(ctx context.Context, p domain.Person, password *authgo.PasswordHash) error {
	var hash pgtype.Text
	if password != nil {
		hash = pgtype.Text{String: password.String(), Valid: true}
	}
	return storeError(s.q.InsertPerson(ctx, identitysql.InsertPersonParams{
		ID:                 p.ID.UUID(),
		Email:              p.Email.String(),
		DisplayName:        p.DisplayName,
		IndividualTenantID: p.IndividualTenantID.UUID(),
		PasswordHash:       hash,
		EmailVerifiedAt:    timestamptz(p.EmailVerifiedAt),
		CreatedAt:          p.CreatedAt,
	}))
}

func (s *systemStore) PersonByEmail(ctx context.Context, email authgo.Email) (app.PersonRecord, error) {
	row, err := s.q.GetPersonByEmail(ctx, email.String())
	if err != nil {
		return app.PersonRecord{}, storeError(err)
	}
	return personRecord(identitysql.GetPersonByIDRow(row))
}

func (s *systemStore) PersonByID(ctx context.Context, id domain.PersonID) (app.PersonRecord, error) {
	row, err := s.q.GetPersonByID(ctx, id.UUID())
	if err != nil {
		return app.PersonRecord{}, storeError(err)
	}
	return personRecord(row)
}

func personRecord(row identitysql.GetPersonByIDRow) (app.PersonRecord, error) {
	email, err := authgo.NewEmail(row.Email)
	if err != nil {
		return app.PersonRecord{}, fmt.Errorf("identity: stored email %q: %w", row.Email, err)
	}
	rec := app.PersonRecord{
		Person: domain.Person{
			ID:                 domain.PersonID(row.ID),
			Email:              email,
			DisplayName:        row.DisplayName,
			IndividualTenantID: tenancy.ID(row.IndividualTenantID),
			EmailVerifiedAt:    timePtr(row.EmailVerifiedAt),
			CreatedAt:          row.CreatedAt.UTC(),
			UpdatedAt:          row.UpdatedAt.UTC(),
		},
		TOTPEnabled: row.TotpEnabled,
	}
	if row.PasswordHash.Valid {
		h, err := authgo.PasswordHashFromString(row.PasswordHash.String)
		if err != nil {
			return app.PersonRecord{}, fmt.Errorf("identity: stored password hash of %s: %w", row.ID, err)
		}
		rec.PasswordHash = &h
	}
	return rec, nil
}

func (s *systemStore) MarkEmailVerified(ctx context.Context, id domain.PersonID, at time.Time) error {
	return storeError(s.q.MarkEmailVerified(ctx, identitysql.MarkEmailVerifiedParams{ID: id.UUID(), At: at}))
}

func (s *systemStore) SetPassword(ctx context.Context, id domain.PersonID, hash authgo.PasswordHash, at time.Time) error {
	return storeError(s.q.SetPasswordHash(ctx, identitysql.SetPasswordHashParams{
		ID: id.UUID(), PasswordHash: pgtype.Text{String: hash.String(), Valid: true}, At: at,
	}))
}

func (s *systemStore) ActiveMembership(ctx context.Context, person domain.PersonID, tenant tenancy.ID) (app.MembershipGrant, error) {
	row, err := s.q.SystemGetActiveMembership(ctx, identitysql.SystemGetActiveMembershipParams{
		PersonID: nullUUID(person.UUID()), TenantID: tenant.UUID(),
	})
	if err != nil {
		return app.MembershipGrant{}, storeError(err)
	}
	roles, locales, err := access(row.Roles, row.Locales)
	if err != nil {
		return app.MembershipGrant{}, err
	}
	projects, err := projectScope(row.Projects)
	if err != nil {
		return app.MembershipGrant{}, fmt.Errorf("identity: stored project scope of member %s: %w", row.ID, err)
	}
	visibility, err := domain.ParseVisibility(row.Visibility)
	if err != nil {
		return app.MembershipGrant{}, fmt.Errorf("identity: stored visibility of member %s: %w", row.ID, err)
	}
	return app.MembershipGrant{
		Member: domain.MemberID(row.ID), Roles: roles, Locales: locales, Projects: projects, Visibility: visibility,
	}, nil
}

func access(roles, locales []string) (domain.Roles, domain.LocaleScope, error) {
	r, err := domain.ParseRoles(roles)
	if err != nil {
		return nil, domain.LocaleScope{}, fmt.Errorf("identity: stored roles: %w", err)
	}
	l, err := domain.ParseLocaleScope(locales)
	if err != nil {
		return nil, domain.LocaleScope{}, fmt.Errorf("identity: stored locales: %w", err)
	}
	return r, l, nil
}

func (s *systemStore) MembershipsOf(ctx context.Context, person domain.PersonID, after tenancy.ID, limit int) ([]app.MembershipView, error) {
	rows, err := s.q.SystemListMembershipsOfPerson(ctx, identitysql.SystemListMembershipsOfPersonParams{
		PersonID: nullUUID(person.UUID()), After: after.UUID(), MaxRows: int32(limit), //nolint:gosec // bounded by pagination
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]app.MembershipView, 0, len(rows))
	for _, r := range rows {
		roles, locales, err := access(r.Roles, r.Locales)
		if err != nil {
			return nil, err
		}
		out = append(out, app.MembershipView{
			Member: domain.MemberID(r.MemberID),
			Tenant: tenancy.Tenant{
				ID: tenancy.ID(r.TenantID), Kind: tenancy.Kind(r.Kind), Slug: tenancy.Slug(r.Slug),
				Name: r.Name, CreatedAt: r.CreatedAt.UTC(),
			},
			Roles:   roles,
			Locales: locales,
		})
	}
	return out, nil
}

func (s *systemStore) OpenInvitations(ctx context.Context, email authgo.Email) ([]app.InvitationRef, error) {
	rows, err := s.q.SystemListOpenInvitations(ctx, email.String())
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]app.InvitationRef, len(rows))
	for i, r := range rows {
		out[i] = app.InvitationRef{Member: domain.MemberID(r.ID), Tenant: tenancy.ID(r.TenantID)}
	}
	return out, nil
}

func (s *systemStore) Tenant(ctx context.Context, id tenancy.ID) (tenancy.Tenant, error) {
	row, err := s.q.SystemGetTenant(ctx, id.UUID())
	if err != nil {
		return tenancy.Tenant{}, storeError(err)
	}
	return tenancy.Tenant{
		ID: tenancy.ID(row.ID), Kind: tenancy.Kind(row.Kind), Slug: tenancy.Slug(row.Slug),
		Name: row.Name, CreatedAt: row.CreatedAt.UTC(),
	}, nil
}

func (s *systemStore) TokenByHash(ctx context.Context, hash string) (app.TokenRecord, error) {
	row, err := s.q.SystemGetTokenByHash(ctx, hash)
	if err != nil {
		return app.TokenRecord{}, storeError(err)
	}
	scopes, err := domain.ParseScopes(row.Scopes)
	if err != nil {
		return app.TokenRecord{}, fmt.Errorf("identity: stored scopes of token %s: %w", row.ID, err)
	}
	projects, err := projectScope(row.Projects)
	if err != nil {
		return app.TokenRecord{}, fmt.Errorf("identity: stored project scope of token %s: %w", row.ID, err)
	}
	return app.TokenRecord{
		ID: domain.TokenID(row.ID), Tenant: tenancy.ID(row.TenantID), Scopes: scopes, Projects: projects,
		ExpiresAt: timePtr(row.ExpiresAt), RevokedAt: timePtr(row.RevokedAt), LastUsedAt: timePtr(row.LastUsedAt),
	}, nil
}

func (s *systemStore) TouchToken(ctx context.Context, id domain.TokenID, at time.Time) error {
	return storeError(s.q.SystemTouchToken(ctx, identitysql.SystemTouchTokenParams{ID: id.UUID(), At: timestamptz(&at)}))
}

func (s *systemStore) PendingTOTP(ctx context.Context, person domain.PersonID, secret authgo.TOTPSecret, at time.Time) error {
	sealed, err := seal(s.cipher, secret)
	if err != nil {
		return err
	}
	n, err := s.q.UpsertPendingTOTP(ctx, identitysql.UpsertPendingTOTPParams{
		PersonID: person.UUID(), SecretCiphertext: sealed, At: at,
	})
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrTOTPAlreadyEnabled
	}
	return nil
}

func (s *systemStore) TOTP(ctx context.Context, person domain.PersonID) (app.TOTPRecord, error) {
	row, err := s.q.GetTOTP(ctx, person.UUID())
	if err != nil {
		return app.TOTPRecord{}, storeError(err)
	}
	return app.TOTPRecord{Confirmed: row.ConfirmedAt.Valid}, nil
}

func (s *systemStore) ConfirmTOTP(ctx context.Context, person domain.PersonID, at time.Time) error {
	n, err := s.q.ConfirmTOTP(ctx, identitysql.ConfirmTOTPParams{PersonID: person.UUID(), At: timestamptz(&at)})
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrTOTPNotPending
	}
	return nil
}

func (s *systemStore) DeleteTOTP(ctx context.Context, person domain.PersonID) error {
	_, err := s.q.DeleteTOTP(ctx, person.UUID())
	return storeError(err)
}

func (s *systemStore) AddPasskey(ctx context.Context, person domain.PersonID, c authgo.PasskeyCredential, at time.Time) error {
	return storeError(s.q.InsertPasskey(ctx, identitysql.InsertPasskeyParams{
		CredentialID: c.ID, PersonID: person.UUID(), PublicKey: c.PublicKey,
		SignCount: int64(c.SignCount), Name: c.Name, CreatedAt: at,
	}))
}

func (s *systemStore) SaveCeremony(ctx context.Context, c app.Ceremony) error {
	return storeError(s.q.InsertCeremony(ctx, identitysql.InsertCeremonyParams{
		KeyHash: c.KeyHash, Purpose: c.Purpose, PersonID: nullUUID(c.Person.UUID()),
		State: c.State, ExpiresAt: c.ExpiresAt,
	}))
}

func (s *systemStore) TakeCeremony(ctx context.Context, keyHash, purpose string) (app.Ceremony, error) {
	row, err := s.q.TakeCeremony(ctx, identitysql.TakeCeremonyParams{KeyHash: keyHash, Purpose: purpose})
	if err != nil {
		return app.Ceremony{}, storeError(err)
	}
	return app.Ceremony{
		KeyHash: keyHash, Purpose: purpose, Person: domain.PersonID(row.PersonID.UUID),
		State: row.State, ExpiresAt: row.ExpiresAt.UTC(),
	}, nil
}

func (s *systemStore) PersonOfPasskey(ctx context.Context, credentialID []byte) (domain.PersonID, error) {
	row, err := s.q.GetPasskey(ctx, credentialID)
	if err != nil {
		return domain.PersonID{}, storeError(err)
	}
	return domain.PersonID(row.PersonID), nil
}

func (s *systemStore) PasskeysOf(ctx context.Context, person domain.PersonID, after app.PasskeyCursor, limit int) ([]app.Passkey, error) {
	rows, err := s.q.PagePasskeysOfPerson(ctx, identitysql.PagePasskeysOfPersonParams{
		PersonID: person.UUID(), AfterCreatedAt: after.CreatedAt, AfterID: after.ID,
		MaxRows: int32(limit), //nolint:gosec // page sizes are small
	})
	if err != nil {
		return nil, storeError(err)
	}
	out := make([]app.Passkey, len(rows))
	for i, r := range rows {
		out[i] = app.Passkey{ID: r.CredentialID, Name: r.Name, CreatedAt: r.CreatedAt.UTC(), LastUsedAt: timePtr(r.LastUsedAt)}
	}
	return out, nil
}

func (s *systemStore) DeletePasskeyOf(ctx context.Context, person domain.PersonID, credentialID []byte) error {
	n, err := s.q.DeletePasskeyOfPerson(ctx, identitysql.DeletePasskeyOfPersonParams{
		CredentialID: credentialID, PersonID: person.UUID(),
	})
	if err != nil {
		return storeError(err)
	}
	if n == 0 {
		return app.ErrNotFound
	}
	return nil
}
