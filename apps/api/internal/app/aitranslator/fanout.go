// Package aitranslatorapp wires the AI-translator fan-out use case:
// when a source-locale translation lands, asynchronously produce
// ai_translated rows for every other enabled locale that doesn't
// already have a reviewer-touched entry.
package aitranslatorapp

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/apps/api/internal/domain/aitranslator"
	"go.klarlabs.de/glossa/apps/api/internal/domain/analytics"
	"go.klarlabs.de/glossa/apps/api/internal/domain/audit"
	"go.klarlabs.de/glossa/apps/api/internal/domain/locale"
	"go.klarlabs.de/glossa/apps/api/internal/domain/translation"
)

// FanOutInput describes one source-locale write that may need to
// produce N target-locale ai_translated rows.
type FanOutInput struct {
	TenantID       uuid.UUID
	ProjectID      uuid.UUID
	KeyID          uuid.UUID
	KeyName        string
	SourceLocaleID uuid.UUID
	SourceLocale   string // BCP-47 code
	SourceValue    string
}

// Sealer is the encryption port the fan-out uses to decrypt provider
// keys before calling out.
type Sealer interface {
	Open(ct, nonce []byte) ([]byte, error)
}

// FanOut is the use case.
type FanOut struct {
	providers    aitranslator.Repository
	locales      locale.Repository
	translations translation.Repository
	audits       audit.Repository
	translator   aitranslator.Translator
	sealer       Sealer
	analytics    analytics.Recorder
	log          *slog.Logger
}

// New wires the use case. analytics may be nil — the recorder is
// best-effort and only used to record successful ai_translation
// events for the metrics funnel.
func New(
	providers aitranslator.Repository,
	locales locale.Repository,
	translations translation.Repository,
	audits audit.Repository,
	translator aitranslator.Translator,
	sealer Sealer,
	analytics analytics.Recorder,
	log *slog.Logger,
) *FanOut {
	if log == nil {
		log = slog.Default()
	}
	return &FanOut{
		providers:    providers,
		locales:      locales,
		translations: translations,
		audits:       audits,
		translator:   translator,
		sealer:       sealer,
		analytics:    analytics,
		log:          log,
	}
}

// Trigger is the fire-and-forget entry point: spawns a goroutine
// with a detached context so the source upsert returns immediately.
func (f *FanOut) Trigger(in FanOutInput) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := f.Execute(ctx, in); err != nil {
			f.log.Warn("ai fan-out failed",
				"tenant", in.TenantID,
				"project", in.ProjectID,
				"key", in.KeyName,
				"err", err,
			)
		}
	}()
}

// Execute runs the fan-out synchronously. Exposed for tests + the
// "translate missing keys" backfill button.
func (f *FanOut) Execute(ctx context.Context, in FanOutInput) error {
	if f.sealer == nil {
		return errors.New("aitranslatorapp: no sealer configured")
	}
	provs, err := f.providers.ListEnabled(ctx, in.TenantID)
	if err != nil {
		return err
	}
	if len(provs) == 0 {
		return nil
	}
	// Round-robin would be more fair but for v1 prefer the
	// oldest-created enabled provider — stable behavior across runs.
	prov := provs[0]
	key, err := f.sealer.Open(prov.APIKeyCT, prov.APIKeyNonce)
	if err != nil {
		return err
	}

	locs, err := f.locales.ListForProject(ctx, in.ProjectID)
	if err != nil {
		return err
	}

	for _, loc := range locs {
		if !loc.Enabled || loc.ID == in.SourceLocaleID {
			continue
		}
		if err := f.translateOne(ctx, prov, key, in, loc); err != nil {
			f.log.Warn("ai translate skipped",
				"key", in.KeyName,
				"locale", loc.Code,
				"err", err,
			)
		}
	}
	return nil
}

func (f *FanOut) translateOne(
	ctx context.Context,
	prov aitranslator.Provider,
	key []byte,
	in FanOutInput,
	loc locale.Locale,
) error {
	existing, err := f.translations.Find(ctx, in.KeyID, loc.ID)
	if err == nil {
		switch existing.Status {
		case translation.StatusApproved, translation.StatusNeedsReview:
			return nil
		}
	} else if !errors.Is(err, translation.ErrNotFound) {
		return err
	}

	res, err := f.translator.Translate(ctx, prov, key, aitranslator.TranslateRequest{
		Key:          in.KeyName,
		SourceLocale: in.SourceLocale,
		TargetLocale: loc.Code.String(),
		Source:       in.SourceValue,
	})
	if err != nil {
		return err
	}

	upserted, err := f.translations.Upsert(ctx, translation.Translation{
		KeyID:    in.KeyID,
		LocaleID: loc.ID,
		Value:    res.Translation,
		Status:   translation.StatusAITranslated,
	})
	if err != nil {
		return err
	}

	if err := f.audits.Append(ctx, audit.Entry{
		TenantID:      in.TenantID,
		TranslationID: upserted.ID,
		BeforeValue:   existing.Value,
		AfterValue:    res.Translation,
		ActorKind:     "ai",
		ActorLabel:    res.Provider,
	}); err != nil {
		return err
	}

	if f.analytics != nil {
		pid := in.ProjectID
		_ = f.analytics.Record(ctx, analytics.Event{
			TenantID:  in.TenantID,
			ProjectID: &pid,
			Kind:      analytics.KindAITranslation,
			Metadata: map[string]any{
				"key":      in.KeyName,
				"locale":   loc.Code.String(),
				"provider": res.Provider,
			},
		})
	}
	return nil
}
