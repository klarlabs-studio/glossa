//go:build integration

package app

import (
	"context"

	"go.klarlabs.de/glossa/platform/internal/kernel/outbox"
)

// HandleTranslationEvents lets the external tests hand the derive_tm
// subscriber a batch directly.
func HandleTranslationEvents(s *Service, ctx context.Context, ds []outbox.Delivery) []error {
	return s.handleTranslationEvents(ctx, ds)
}
