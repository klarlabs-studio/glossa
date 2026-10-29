// Package audit adapts the Audit context's Recorder to Identity's
// SignInAudit port: Identity says who tried to sign in, how, and whether
// it worked, and the Audit context writes it into the tenant's chain.
package audit

import (
	"context"

	auditapp "go.klarlabs.de/glossa/platform/internal/audit/app"
	auditdomain "go.klarlabs.de/glossa/platform/internal/audit/domain"
	"go.klarlabs.de/glossa/platform/internal/identity/app"
)

// SignIns implements app.SignInAudit.
type SignIns struct{ rec auditapp.Recorder }

// New returns the adapter over rec.
func New(rec auditapp.Recorder) *SignIns { return &SignIns{rec: rec} }

var _ app.SignInAudit = (*SignIns)(nil)

// RecordSignIn implements app.SignInAudit.
func (s *SignIns) RecordSignIn(ctx context.Context, a app.SignInAttempt) error {
	return s.rec.RecordSignIn(ctx, auditdomain.SignIn{
		Attempt: a.ID, Person: a.Person.UUID(), Method: a.Method, Failure: a.Failure, At: a.At,
	})
}
