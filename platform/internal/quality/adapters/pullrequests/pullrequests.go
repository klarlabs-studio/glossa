// Package pullrequests adapts Integration's GitHub service to Quality's
// PullRequestLinks port: where a project's pull requests are on the web,
// so the policy impact preview (RFC 0005 §4.3) can link to the one a
// candidate would newly fail rather than print a number to look up.
//
// Quality reads no other context's tables (RFC 0002 §4); it asks
// Integration, which knows the repository connected to the project and
// the web host of the GitHub App.
package pullrequests

import (
	"context"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/quality/app"
)

// Source is the part of Integration's GitHub service this reads.
type Source interface {
	PullRequestURLs(ctx context.Context, project uuid.UUID, numbers []int) (map[int]string, error)
}

// Port implements app.PullRequestLinks.
type Port struct{ src Source }

var _ app.PullRequestLinks = (*Port)(nil)

// New returns the port over src.
func New(src Source) *Port { return &Port{src: src} }

// PullRequestURLs implements app.PullRequestLinks.
func (p *Port) PullRequestURLs(ctx context.Context, project uuid.UUID, numbers []int) (map[int]string, error) {
	return p.src.PullRequestURLs(ctx, project, numbers)
}
