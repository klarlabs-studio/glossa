package domain

import (
	"crypto/rand"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	authgo "github.com/klarlabs-studio/auth-go/domain"

	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

const maxDisplayNameLen = 200

// Person is a human who can sign in. People are global to the
// deployment: one person, one email, any number of tenants.
type Person struct {
	ID          PersonID
	Email       authgo.Email
	DisplayName string
	// IndividualTenantID is reserved at registration for the person's
	// own tenant, so creating that tenant can be retried until it exists.
	IndividualTenantID tenancy.ID
	EmailVerifiedAt    *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// NewPerson validates the inputs and returns a person with fresh IDs.
func NewPerson(email authgo.Email, displayName string, now time.Time) (Person, error) {
	name, err := parseDisplayName(displayName)
	if err != nil {
		return Person{}, err
	}
	return Person{
		ID:                 NewPersonID(),
		Email:              email,
		DisplayName:        name,
		IndividualTenantID: tenancy.NewID(),
		CreatedAt:          now,
		UpdatedAt:          now,
	}, nil
}

func parseDisplayName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > maxDisplayNameLen {
		return "", fmt.Errorf("%w: at most %d characters", ErrInvalidDisplayName, maxDisplayNameLen)
	}
	return s, nil
}

// EmailVerified reports whether the person proved they own the address.
func (p Person) EmailVerified() bool { return p.EmailVerifiedAt != nil }

// IndividualTenant describes the person's own tenant: kind individual,
// named after them, with a slug derived from their email plus a random
// suffix so it never collides and never needs to be chosen.
func (p Person) IndividualTenant() (tenancy.Tenant, error) {
	local, _, _ := strings.Cut(p.Email.String(), "@")
	name := p.DisplayName
	if name == "" {
		name = local
	}
	t, err := tenancy.NewTenant(tenancy.KindIndividual, slugBase(local)+"-"+randomSuffix(), name)
	if err != nil {
		return tenancy.Tenant{}, err
	}
	t.ID = p.IndividualTenantID
	return t, nil
}

// slugBase turns an email local part into slug characters, 40 at most.
func slugBase(local string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(local) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "user"
	}
	return s
}

const suffixAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"

func randomSuffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b) // crypto/rand.Read never fails (Go 1.24+)
	for i := range b {
		b[i] = suffixAlphabet[int(b[i])%len(suffixAlphabet)]
	}
	return string(b)
}
