package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/problem"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

func TestOwnTextIsAForbiddenProblem(t *testing.T) {
	err := mapError(fmt.Errorf("review: %w", domain.ErrOwnText))
	var p *problem.Details
	if !errors.As(err, &p) {
		t.Fatalf("not a problem: %v", err)
	}
	if p.Status != http.StatusForbidden || string(p.Code) != "own_text" {
		t.Errorf("own text = %d %s, want 403 own_text", p.Status, p.Code)
	}
}
