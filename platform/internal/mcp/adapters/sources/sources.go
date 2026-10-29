// Package sources adapts the bounded contexts' application services to
// the narrow ports MCP's read tools declare (RFC 0005 §7.1: every tool
// is a thin call into an application port, never a second
// implementation of a rule).
//
// Everything context-specific lives here: the id and locale types, the
// domain shapes, and each context's own not-found error, which every
// adapter translates into the one answer MCP gives — a thing this
// tenant cannot see is not found, never forbidden-with-detail.
package sources

import (
	"errors"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/identity/authz"
	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/tools"
)

// notFound maps a context's own not-found errors onto MCP's. Anything
// else is passed through: a storage failure is not a missing message,
// and saying so would send an agent looking for a typo.
func notFound(err error, kinds ...error) error {
	// A project outside the token's scope is, to the agent, a project
	// that does not exist (RFC 0006 §4.1).
	if errors.Is(err, authz.ErrNotVisible) {
		return tools.ErrNotFound
	}
	for _, kind := range kinds {
		if errors.Is(err, kind) {
			return tools.ErrNotFound
		}
	}
	// A 404 problem from a context that answers in HTTP terms is the
	// same statement in another vocabulary.
	var d *problem.Details
	if errors.As(err, &d) && d.Status == http.StatusNotFound {
		return tools.ErrNotFound
	}
	return err
}

// page turns a tool's cursor and limit into the API's page request. A
// cursor this platform never issued is an invalid argument, not an
// empty result: silently restarting at the first page would make an
// agent loop forever.
func page(cursor string, limit int) (pagination.Page, error) {
	p, err := pagination.Parse(&limit, &cursor)
	if err != nil {
		return pagination.Page{}, &app.InvalidArgumentError{
			Argument: "cursor", Reason: "not a cursor this tool issued",
		}
	}
	return p, nil
}

// next dereferences the API's next-page token.
func next(token *string) string {
	if token == nil {
		return ""
	}
	return *token
}
