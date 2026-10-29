package project_test

import (
	"errors"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/apps/api/internal/domain/project"
)

func TestNewSlug(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		wantOK bool
	}{
		{"single lowercase", "x", true},
		{"alnum mix", "iri", true},
		{"with hyphens", "brotwerk-web", true},
		{"trailing digits", "app2", true},
		{"empty", "", false},
		{"leading hyphen", "-app", false},
		{"trailing hyphen", "app-", false},
		{"uppercase", "APP", false},
		{"underscore", "app_name", false},
		{"too long", strings.Repeat("a", 51), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := project.NewSlug(tc.input)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("expected ok, got err: %v", err)
				}
				if got.String() != tc.input {
					t.Fatalf("slug round-trip mismatch: got %q want %q", got, tc.input)
				}
				return
			}
			if !errors.Is(err, project.ErrInvalidSlug) {
				t.Fatalf("expected ErrInvalidSlug, got %v", err)
			}
		})
	}
}

func TestNewName(t *testing.T) {
	if _, err := project.NewName(""); !errors.Is(err, project.ErrInvalidName) {
		t.Fatalf("empty name: expected ErrInvalidName, got %v", err)
	}
	if _, err := project.NewName(strings.Repeat("x", 201)); !errors.Is(err, project.ErrInvalidName) {
		t.Fatalf("201-char name: expected ErrInvalidName, got %v", err)
	}
	n, err := project.NewName("Brotwerk Web — DE/EN")
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if n.String() != "Brotwerk Web — DE/EN" {
		t.Fatalf("name round-trip mismatch: %q", n)
	}
}
