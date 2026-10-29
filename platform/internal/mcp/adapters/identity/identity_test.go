package identity_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	identityapp "go.klarlabs.de/glossa/platform/internal/identity/app"
	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	identitydomain "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
	mcpidentity "go.klarlabs.de/glossa/platform/internal/mcp/adapters/identity"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// fakeTokens stands in for Identity's service. It records what it was
// asked, so a test can prove a credential was refused *before* it was
// looked up at all.
type fakeTokens struct {
	authn     identityapp.Authn
	authnErr  error
	principal authz.Principal
	authzErr  error
	looked    []string
}

func (f *fakeTokens) AuthenticateToken(_ context.Context, bearer string) (identityapp.Authn, error) {
	f.looked = append(f.looked, bearer)
	return f.authn, f.authnErr
}

func (f *fakeTokens) Authorize(_ context.Context, _ identityapp.Authn, _ tenancy.ID) (authz.Principal, error) {
	return f.principal, f.authzErr
}

func tokenAuthn(t *testing.T, tenant tenancy.ID, scopes ...string) (identityapp.Authn, identitydomain.TokenID) {
	t.Helper()
	ss, err := identitydomain.ParseScopes(scopes)
	if err != nil {
		t.Fatalf("scopes: %v", err)
	}
	id := identitydomain.NewTokenID()
	return identityapp.Authn{
		Actor: identitydomain.TokenActor(id),
		Token: &identityapp.TokenRecord{ID: id, Tenant: tenant, Scopes: ss},
	}, id
}

func TestAuthenticate(t *testing.T) {
	tenant := tenancy.NewID()
	good, tokenID := tokenAuthn(t, tenant, "read", "write")

	tests := []struct {
		name string
		// bearer is what the agent presented.
		bearer string
		tokens *fakeTokens
		// wantErr is the sentinel the refusal must match; nil means the
		// credential is accepted.
		wantErr error
		// wantLookedUp says whether Identity's token table was consulted.
		// A refused credential kind must not be.
		wantLookedUp bool
	}{
		{
			name:         "a tenant API token is accepted",
			bearer:       identitydomain.TokenPrefix + strings.Repeat("a", 43),
			tokens:       &fakeTokens{authn: good, principal: authz.Principal{Actor: good.Actor, Tenant: tenant}},
			wantLookedUp: true,
		},
		{
			name:    "a CI token is refused",
			bearer:  identitydomain.CITokenPrefix + strings.Repeat("a", 43),
			tokens:  &fakeTokens{authn: good},
			wantErr: domain.ErrCredentialNotAccepted,
		},
		{
			name:    "an in-context grant is refused",
			bearer:  identitydomain.InContextGrantPrefix + strings.Repeat("a", 43),
			tokens:  &fakeTokens{authn: good},
			wantErr: domain.ErrCredentialNotAccepted,
		},
		{
			name:         "an unknown, revoked or expired token is unauthenticated",
			bearer:       identitydomain.TokenPrefix + strings.Repeat("b", 43),
			tokens:       &fakeTokens{authnErr: identityapp.ErrUnauthenticated},
			wantErr:      domain.ErrUnauthenticated,
			wantLookedUp: true,
		},
		{
			name:    "an empty bearer is unauthenticated",
			bearer:  "   ",
			tokens:  &fakeTokens{},
			wantErr: domain.ErrUnauthenticated,
		},
		{
			name:         "a session cookie's authn is not a token",
			bearer:       identitydomain.TokenPrefix + strings.Repeat("c", 43),
			tokens:       &fakeTokens{authn: identityapp.Authn{Person: identitydomain.NewPersonID()}},
			wantErr:      domain.ErrUnauthenticated,
			wantLookedUp: true,
		},
		{
			name:         "a token whose tenant refuses it is unauthenticated",
			bearer:       identitydomain.TokenPrefix + strings.Repeat("d", 43),
			tokens:       &fakeTokens{authn: good, authzErr: identityapp.ErrForbidden},
			wantErr:      domain.ErrUnauthenticated,
			wantLookedUp: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			caller, err := mcpidentity.New(tc.tokens).Authenticate(context.Background(), tc.bearer)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				if err.Error() == tc.wantErr.Error() {
					t.Errorf("the refusal adds no explanation: %q", err)
				}
			} else {
				if err != nil {
					t.Fatalf("err = %v", err)
				}
				if caller.Tenant != tenant || caller.Token != tokenID {
					t.Errorf("caller = %+v, want tenant %s and token %s", caller, tenant, tokenID)
				}
				if got := caller.Scopes.Strings(); len(got) != 2 {
					t.Errorf("scopes = %v, want read and write", got)
				}
			}
			if looked := len(tc.tokens.looked) > 0; looked != tc.wantLookedUp {
				t.Errorf("token table consulted = %t, want %t", looked, tc.wantLookedUp)
			}
		})
	}
}
