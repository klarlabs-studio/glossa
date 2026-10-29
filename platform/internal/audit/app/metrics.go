package app

import (
	"errors"

	"go.klarlabs.de/glossa/platform/internal/audit/domain"
)

// Metrics records the Audit context's operational numbers (RFC 0006
// §10.1). Nothing here carries a tenant, an actor or a summary: only
// counts, and the closed outcome vocabulary of an export job.
type Metrics interface {
	// Appended counts entries appended to a chain, once their
	// transaction committed.
	Appended(n int)
	// VerifyFailed counts a chain verification that found an entry not
	// following the one before it.
	VerifyFailed()
	// ExportJob counts an export job that ended, by ExportSucceeded or
	// ExportFailed.
	ExportJob(outcome string)
}

// The export job outcomes glossa_audit_export_jobs_total is labelled by.
const (
	ExportSucceeded = "succeeded"
	ExportFailed    = "failed"
)

// WithMetrics records appends, verification failures and export jobs.
func WithMetrics(m Metrics) Option { return func(s *Service) { s.metrics = m } }

// Metrics is what the service records with, nil when none is wired; the
// export jobs record their outcome through it.
func (s *Service) Metrics() Metrics { return s.metrics }

// appended records n appended entries.
func (s *Service) appended(n int) {
	if s.metrics != nil && n > 0 {
		s.metrics.Appended(n)
	}
}

// verified records a verification's outcome: only a broken chain is
// counted, never a storage error that stopped the read.
func (s *Service) verified(err error) {
	var ce *domain.ChainError
	if s.metrics != nil && errors.As(err, &ce) {
		s.metrics.VerifyFailed()
	}
}
