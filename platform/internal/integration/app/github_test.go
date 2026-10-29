package app_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"go.klarlabs.de/glossa/platform/internal/integration/app"
)

type rateLimited struct{ wait time.Duration }

func (e rateLimited) Error() string             { return "rate limited" }
func (e rateLimited) RetryAfter() time.Duration { return e.wait }
func (e rateLimited) Unwrap() error             { return app.ErrGitHubRateLimited }

func TestGitHubRetryAfter(t *testing.T) {
	err := fmt.Errorf("checks.update: %w", rateLimited{wait: 90 * time.Second})
	if d, ok := app.GitHubRetryAfter(err); !ok || d != 90*time.Second {
		t.Fatalf("GitHubRetryAfter = %v, %v; want 90s, true", d, ok)
	}
	if !errors.Is(err, app.ErrGitHubRateLimited) {
		t.Fatal("the wrapped error lost its sentinel")
	}
	if _, ok := app.GitHubRetryAfter(app.ErrGitHubUnavailable); ok {
		t.Fatal("a plain error carries no wait")
	}
	if _, ok := app.GitHubRetryAfter(rateLimited{}); ok {
		t.Fatal("a zero wait is no wait")
	}
}
