package tenancy_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

func TestNewTenant(t *testing.T) {
	tests := []struct {
		name    string
		kind    tenancy.Kind
		slug    string
		tname   string
		wantErr error
	}{
		{"individual", tenancy.KindIndividual, "ada", "Ada Lovelace", nil},
		{"organization", tenancy.KindOrganization, "klar-labs-2", "Klarlabs", nil},
		{"unknown kind", tenancy.Kind("team"), "ada", "Ada", tenancy.ErrInvalidKind},
		{"uppercase slug", tenancy.KindIndividual, "Ada", "Ada", tenancy.ErrInvalidSlug},
		{"leading hyphen", tenancy.KindIndividual, "-ada", "Ada", tenancy.ErrInvalidSlug},
		{"trailing hyphen", tenancy.KindIndividual, "ada-", "Ada", tenancy.ErrInvalidSlug},
		{"too long slug", tenancy.KindIndividual, strings.Repeat("a", 64), "Ada", tenancy.ErrInvalidSlug},
		{"empty name", tenancy.KindIndividual, "ada", "  ", tenancy.ErrInvalidName},
		{"too long name", tenancy.KindIndividual, "ada", strings.Repeat("n", 201), tenancy.ErrInvalidName},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tenancy.NewTenant(tc.kind, tc.slug, tc.tname)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if got.ID.IsZero() {
				t.Error("new tenant has a zero ID")
			}
			if got.Kind != tc.kind || string(got.Slug) != tc.slug || got.Name != strings.TrimSpace(tc.tname) {
				t.Errorf("tenant = %+v", got)
			}
		})
	}
}

func TestParseID(t *testing.T) {
	id := tenancy.NewID()
	parsed, err := tenancy.ParseID(id.String())
	if err != nil || parsed != id {
		t.Fatalf("ParseID(%q) = %v, %v", id, parsed, err)
	}
	if _, err := tenancy.ParseID("not-a-uuid"); !errors.Is(err, tenancy.ErrInvalidID) {
		t.Errorf("ParseID(garbage) err = %v, want ErrInvalidID", err)
	}
	if _, err := tenancy.ParseID("00000000-0000-0000-0000-000000000000"); !errors.Is(err, tenancy.ErrInvalidID) {
		t.Errorf("ParseID(nil uuid) err = %v, want ErrInvalidID", err)
	}
}

func TestContextRoundTrip(t *testing.T) {
	ctx := context.Background()
	if _, ok := tenancy.FromContext(ctx); ok {
		t.Fatal("empty context carries a tenant")
	}
	id := tenancy.NewID()
	got, ok := tenancy.FromContext(tenancy.ContextWithTenant(ctx, id))
	if !ok || got != id {
		t.Fatalf("FromContext = %v, %v; want %v", got, ok, id)
	}
	if _, ok := tenancy.FromContext(tenancy.ContextWithTenant(ctx, tenancy.ID{})); ok {
		t.Error("a zero ID must not count as a tenant")
	}
}
