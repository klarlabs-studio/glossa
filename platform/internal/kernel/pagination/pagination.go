// Package pagination implements the API's cursor pagination convention
// (api/openapi.yaml, "Pagination"): a request carries page_size and an
// opaque page_token; a response carries next_page_token while there is
// more. Lists are keyset-paginated on a stable, unique sort key, so
// pages don't shift when rows are inserted behind the cursor.
//
//	page, err := pagination.Parse(params.PageSize, params.PageToken)
//	rows := q.ListThings(ctx, page.After, page.Limit())   // WHERE key > after ORDER BY key LIMIT n
//	items, next := pagination.Trim(rows, page, func(r Row) string { return r.ID.String() })
package pagination

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

const (
	// DefaultPageSize applies when page_size is absent.
	DefaultPageSize = 50
	// MaxPageSize is the largest page_size accepted.
	MaxPageSize = 100

	tokenVersion = "v1."
)

// Page is a validated page request.
type Page struct {
	// Size is the number of items to return.
	Size int
	// After is the sort key of the last item of the previous page; ""
	// for the first page.
	After string
}

// Limit is the number of rows to fetch: one more than Size, so Trim can
// tell whether another page follows.
func (p Page) Limit() int { return p.Size + 1 }

// Parse validates the page_size and page_token query parameters.
func Parse(size *int, token *string) (Page, error) {
	p := Page{Size: DefaultPageSize}
	if size != nil {
		if *size < 1 || *size > MaxPageSize {
			return Page{}, problem.New(http.StatusBadRequest, "invalid_page_size",
				"page_size must be between 1 and 100")
		}
		p.Size = *size
	}
	if token == nil || *token == "" {
		return p, nil
	}
	after, err := decode(*token)
	if err != nil {
		return Page{}, problem.New(http.StatusBadRequest, "invalid_page_token",
			"page_token is not one this API issued")
	}
	p.After = after
	return p, nil
}

// Trim cuts rows (fetched with Page.Limit) to the page size and returns
// the token for the next page, or nil on the last page.
func Trim[T any](rows []T, p Page, key func(T) string) ([]T, *string) {
	if len(rows) <= p.Size {
		return rows, nil
	}
	rows = rows[:p.Size]
	next := encode(key(rows[len(rows)-1]))
	return rows, &next
}

// Token is the next_page_token for a page whose store computed its own
// sort key — one that pages without Trim — or nil when after is "" (the
// last page). Parse reads it back into Page.After.
func Token(after string) *string {
	if after == "" {
		return nil
	}
	t := encode(after)
	return &t
}

func encode(after string) string {
	return tokenVersion + base64.RawURLEncoding.EncodeToString([]byte(after))
}

func decode(token string) (string, error) {
	raw, ok := strings.CutPrefix(token, tokenVersion)
	if !ok {
		return "", errInvalid
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(b) == 0 {
		return "", errInvalid
	}
	return string(b), nil
}

var errInvalid = errors.New("pagination: invalid token")
