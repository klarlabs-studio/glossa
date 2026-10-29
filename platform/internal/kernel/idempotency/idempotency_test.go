package idempotency_test

import (
	"errors"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/idempotency"
)

func TestIDIsDeterministicAndScoped(t *testing.T) {
	a := idempotency.ID("project.create", "t1", "person:1", "k")
	if a != idempotency.ID("project.create", "t1", "person:1", "k") {
		t.Error("same inputs, different IDs")
	}
	for _, other := range [][4]string{
		{"message.create", "t1", "person:1", "k"},
		{"project.create", "t2", "person:1", "k"},
		{"project.create", "t1", "person:2", "k"},
		{"project.create", "t1", "person:1", "k2"},
	} {
		if idempotency.ID(other[0], other[1], other[2], other[3]) == a {
			t.Errorf("%v collides", other)
		}
	}
}

func TestCheckKey(t *testing.T) {
	if err := idempotency.CheckKey("0192f0c4-abc"); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{"", "has space", "ünï", strings.Repeat("a", 256)} {
		if err := idempotency.CheckKey(bad); !errors.Is(err, idempotency.ErrInvalidKey) {
			t.Errorf("CheckKey(%q) = %v", bad, err)
		}
	}
}
