package pagination_test

import (
	"errors"
	"net/http"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/pagination"
	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
)

func ptr[T any](v T) *T { return &v }

func TestParse(t *testing.T) {
	tests := []struct {
		name      string
		size      *int
		token     *string
		wantSize  int
		wantAfter string
		wantCode  problem.Code
	}{
		{name: "defaults", wantSize: pagination.DefaultPageSize},
		{name: "explicit size", size: ptr(10), wantSize: 10},
		{name: "max size", size: ptr(pagination.MaxPageSize), wantSize: pagination.MaxPageSize},
		{name: "zero size", size: ptr(0), wantCode: "invalid_page_size"},
		{name: "oversize", size: ptr(pagination.MaxPageSize + 1), wantCode: "invalid_page_size"},
		{name: "empty token is the first page", token: ptr(""), wantSize: pagination.DefaultPageSize},
		{name: "garbage token", token: ptr("not-a-token"), wantCode: "invalid_page_token"},
		{name: "wrong version", token: ptr("v9.YQ"), wantCode: "invalid_page_token"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := pagination.Parse(tc.size, tc.token)
			if tc.wantCode != "" {
				var d *problem.Details
				if !errors.As(err, &d) || d.Code != tc.wantCode || d.Status != http.StatusBadRequest {
					t.Fatalf("err = %v, want a 400 %s problem", err, tc.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if p.Size != tc.wantSize || p.After != tc.wantAfter {
				t.Errorf("page = %+v", p)
			}
		})
	}
}

func TestPaginateRoundTrip(t *testing.T) {
	all := []string{"a", "b", "c", "d", "e"}
	fetch := func(p pagination.Page) []string {
		var out []string
		for _, s := range all {
			if s > p.After && len(out) < p.Limit() {
				out = append(out, s)
			}
		}
		return out
	}

	var seen []string
	p, err := pagination.Parse(ptr(2), nil)
	if err != nil {
		t.Fatal(err)
	}
	for pages := 0; ; pages++ {
		if pages > 5 {
			t.Fatal("pagination does not terminate")
		}
		items, next := pagination.Trim(fetch(p), p, func(s string) string { return s })
		if len(items) > 2 {
			t.Fatalf("page has %d items, want at most 2", len(items))
		}
		seen = append(seen, items...)
		if next == nil {
			break
		}
		if p, err = pagination.Parse(ptr(2), next); err != nil {
			t.Fatalf("next token rejected: %v", err)
		}
	}
	if len(seen) != len(all) {
		t.Errorf("saw %v, want %v", seen, all)
	}
}
