package pagination_test

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
)

// TestTokenRoundTrip: a cursor a store computed itself comes back from
// Parse unchanged, and the last page has no token.
func TestTokenRoundTrip(t *testing.T) {
	if pagination.Token("") != nil {
		t.Error("an empty cursor made a token")
	}
	tok := pagination.Token("2026-10-01T00:00:00Z|0192f5a1")
	p, err := pagination.Parse(nil, tok)
	if err != nil || p.After != "2026-10-01T00:00:00Z|0192f5a1" {
		t.Errorf("Parse(Token) = %+v, %v", p, err)
	}
}
