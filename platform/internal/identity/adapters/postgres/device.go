package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/identity/adapters/postgres/identitysql"
	"go.klarlabs.de/glossa/platform/internal/identity/domain"
)

// Device sign-in (RFC 0006 §7.2, migration 0054): system scope only, as
// sessions and sign-in links are. Only the codes' hashes are stored.

func (s *systemStore) InsertDeviceAuthorization(ctx context.Context, d domain.DeviceAuthorization) error {
	return storeError(s.q.InsertDeviceAuthorization(ctx, identitysql.InsertDeviceAuthorizationParams{
		ID: d.ID.UUID(), DeviceCodeHash: d.DeviceCodeHash, UserCodeHash: d.UserCodeHash, ClientName: d.ClientName,
		IntervalSeconds: int32(d.Interval / time.Second), RequestedAt: d.RequestedAt, ExpiresAt: d.ExpiresAt,
	}))
}

func (s *systemStore) PurgeDeviceAuthorizations(ctx context.Context, before time.Time) (int64, error) {
	n, err := s.q.PurgeDeviceAuthorizations(ctx, before)
	return n, storeError(err)
}

func (s *systemStore) LockPendingDeviceAuthorization(ctx context.Context, userCodeHash string, now time.Time) (domain.DeviceAuthorization, error) {
	row, err := s.q.LockPendingDeviceAuthorizationByUserCode(ctx, identitysql.LockPendingDeviceAuthorizationByUserCodeParams{
		UserCodeHash: userCodeHash, Now: now,
	})
	if err != nil {
		return domain.DeviceAuthorization{}, storeError(err)
	}
	return deviceAuthorization(row), nil
}

func (s *systemStore) LockDeviceAuthorization(ctx context.Context, deviceCodeHash string) (domain.DeviceAuthorization, error) {
	row, err := s.q.LockDeviceAuthorizationByDeviceCode(ctx, deviceCodeHash)
	if err != nil {
		return domain.DeviceAuthorization{}, storeError(err)
	}
	return deviceAuthorization(row), nil
}

func (s *systemStore) UpdateDeviceAuthorization(ctx context.Context, d domain.DeviceAuthorization) error {
	var person uuid.UUID
	if !d.Person.IsZero() {
		person = d.Person.UUID()
	}
	return storeError(s.q.UpdateDeviceAuthorization(ctx, identitysql.UpdateDeviceAuthorizationParams{
		ID: d.ID.UUID(), Status: string(d.Status), PersonID: nullUUID(person),
		IntervalSeconds: int32(d.Interval / time.Second),
		LastPolledAt:    timestamptz(d.LastPolledAt), DecidedAt: timestamptz(d.DecidedAt),
		RedeemedAt: timestamptz(d.RedeemedAt),
	}))
}

func (s *systemStore) WithdrawDeviceApprovals(ctx context.Context, person domain.PersonID) error {
	return storeError(s.q.WithdrawDeviceApprovals(ctx, nullUUID(person.UUID())))
}

func deviceAuthorization(r identitysql.IdentityDeviceAuthorization) domain.DeviceAuthorization {
	d := domain.DeviceAuthorization{
		ID: domain.DeviceAuthorizationID(r.ID), DeviceCodeHash: r.DeviceCodeHash, UserCodeHash: r.UserCodeHash,
		ClientName: r.ClientName, Status: domain.DeviceStatus(r.Status),
		Interval:    time.Duration(r.IntervalSeconds) * time.Second,
		RequestedAt: r.RequestedAt.UTC(), ExpiresAt: r.ExpiresAt.UTC(),
		LastPolledAt: timePtr(r.LastPolledAt), DecidedAt: timePtr(r.DecidedAt), RedeemedAt: timePtr(r.RedeemedAt),
	}
	if r.PersonID.Valid {
		d.Person = domain.PersonID(r.PersonID.UUID)
	}
	return d
}
