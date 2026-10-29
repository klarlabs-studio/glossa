package aitranslatorapp_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/google/uuid"

	aitranslatorapp "go.klarlabs.de/glossa/apps/api/internal/app/aitranslator"
	"go.klarlabs.de/glossa/apps/api/internal/domain/aitranslator"
	"go.klarlabs.de/glossa/apps/api/internal/domain/audit"
	"go.klarlabs.de/glossa/apps/api/internal/domain/locale"
	"go.klarlabs.de/glossa/apps/api/internal/domain/translation"
)

// ── stubs ─────────────────────────────────────────────────────────

type stubProviderRepo struct {
	enabled []aitranslator.Provider
}

func (s *stubProviderRepo) Create(context.Context, aitranslator.Provider) (aitranslator.Provider, error) {
	return aitranslator.Provider{}, nil
}
func (s *stubProviderRepo) List(context.Context, uuid.UUID) ([]aitranslator.Provider, error) {
	return nil, nil
}
func (s *stubProviderRepo) ListEnabled(context.Context, uuid.UUID) ([]aitranslator.Provider, error) {
	return s.enabled, nil
}
func (s *stubProviderRepo) Get(context.Context, uuid.UUID) (aitranslator.Provider, error) {
	return aitranslator.Provider{}, aitranslator.ErrNotFound
}
func (s *stubProviderRepo) Update(context.Context, uuid.UUID, string, string, string, bool) error {
	return nil
}
func (s *stubProviderRepo) UpdateKey(context.Context, uuid.UUID, []byte, []byte) error { return nil }
func (s *stubProviderRepo) Delete(context.Context, uuid.UUID) error                    { return nil }

type stubLocaleRepo struct {
	locales []locale.Locale
}

func (s *stubLocaleRepo) Save(context.Context, locale.Locale) error { return nil }
func (s *stubLocaleRepo) ListForProject(context.Context, uuid.UUID) ([]locale.Locale, error) {
	return s.locales, nil
}
func (s *stubLocaleRepo) SetEnabled(context.Context, uuid.UUID, bool) error { return nil }
func (s *stubLocaleRepo) Delete(context.Context, uuid.UUID) error           { return nil }

type stubTranslationRepo struct {
	existing  map[uuid.UUID]translation.Translation
	upserted  []translation.Translation
	bundleErr error
}

func (s *stubTranslationRepo) Upsert(_ context.Context, t translation.Translation) (translation.Translation, error) {
	t.ID = uuid.New()
	s.upserted = append(s.upserted, t)
	return t, nil
}
func (s *stubTranslationRepo) ListBundle(context.Context, uuid.UUID, uuid.UUID) ([]translation.BundleEntry, error) {
	return nil, s.bundleErr
}
func (s *stubTranslationRepo) Find(_ context.Context, _ uuid.UUID, localeID uuid.UUID) (translation.Translation, error) {
	if t, ok := s.existing[localeID]; ok {
		return t, nil
	}
	return translation.Translation{}, translation.ErrNotFound
}

type stubAuditRepo struct {
	entries []audit.Entry
}

func (s *stubAuditRepo) Append(_ context.Context, e audit.Entry) error {
	s.entries = append(s.entries, e)
	return nil
}
func (s *stubAuditRepo) ListForTenant(context.Context, uuid.UUID, int32, int32) ([]audit.Entry, error) {
	return nil, nil
}

type stubTranslator struct {
	calls []aitranslator.TranslateRequest
}

func (s *stubTranslator) Translate(_ context.Context, p aitranslator.Provider, _ []byte, req aitranslator.TranslateRequest) (aitranslator.TranslateResult, error) {
	s.calls = append(s.calls, req)
	return aitranslator.TranslateResult{
		Translation: "[" + req.TargetLocale + "] " + req.Source,
		Provider:    string(p.Kind),
	}, nil
}

type stubSealer struct{}

func (stubSealer) Open(ct, _ []byte) ([]byte, error) { return ct, nil }

// ── tests ─────────────────────────────────────────────────────────

func TestFanOutTranslatesIntoEveryEnabledNonSourceLocale(t *testing.T) {
	tenantID, projectID, keyID := uuid.New(), uuid.New(), uuid.New()
	sourceID, frID, enID, esID := uuid.New(), uuid.New(), uuid.New(), uuid.New()

	provs := &stubProviderRepo{enabled: []aitranslator.Provider{{
		Kind: aitranslator.KindOpenAI, Model: "x", APIKeyCT: []byte("k"), APIKeyNonce: []byte("n"), Enabled: true,
	}}}
	locs := &stubLocaleRepo{locales: []locale.Locale{
		{ID: sourceID, Code: "de", Enabled: true},
		{ID: frID, Code: "fr", Enabled: true},
		{ID: enID, Code: "en", Enabled: true},
		{ID: esID, Code: "es", Enabled: false}, // disabled — must be skipped
	}}
	trs := &stubTranslationRepo{existing: map[uuid.UUID]translation.Translation{}}
	auds := &stubAuditRepo{}
	tx := &stubTranslator{}

	uc := aitranslatorapp.New(provs, locs, trs, auds, tx, stubSealer{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	err := uc.Execute(context.Background(), aitranslatorapp.FanOutInput{
		TenantID: tenantID, ProjectID: projectID, KeyID: keyID, KeyName: "greeting",
		SourceLocaleID: sourceID, SourceLocale: "de", SourceValue: "Hallo",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(tx.calls) != 2 {
		t.Fatalf("expected 2 translate calls (fr,en), got %d: %+v", len(tx.calls), tx.calls)
	}
	if len(trs.upserted) != 2 {
		t.Fatalf("upserts: got %d want 2", len(trs.upserted))
	}
	for _, u := range trs.upserted {
		if u.Status != translation.StatusAITranslated {
			t.Errorf("status %q want ai_translated", u.Status)
		}
	}
	if len(auds.entries) != 2 {
		t.Fatalf("audit entries: %d want 2", len(auds.entries))
	}
	for _, e := range auds.entries {
		if e.ActorKind != "ai" || e.ActorLabel != "openai" {
			t.Errorf("bad audit actor: %+v", e)
		}
	}
}

func TestFanOutSkipsApprovedAndNeedsReview(t *testing.T) {
	sourceID, frID, enID := uuid.New(), uuid.New(), uuid.New()
	provs := &stubProviderRepo{enabled: []aitranslator.Provider{{Kind: aitranslator.KindOpenAI, APIKeyCT: []byte("k"), APIKeyNonce: []byte("n"), Enabled: true}}}
	locs := &stubLocaleRepo{locales: []locale.Locale{
		{ID: sourceID, Code: "de", Enabled: true},
		{ID: frID, Code: "fr", Enabled: true},
		{ID: enID, Code: "en", Enabled: true},
	}}
	trs := &stubTranslationRepo{existing: map[uuid.UUID]translation.Translation{
		frID: {LocaleID: frID, Value: "Bonjour", Status: translation.StatusApproved},
		enID: {LocaleID: enID, Value: "Hello", Status: translation.StatusNeedsReview},
	}}
	auds := &stubAuditRepo{}
	tx := &stubTranslator{}

	uc := aitranslatorapp.New(provs, locs, trs, auds, tx, stubSealer{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := uc.Execute(context.Background(), aitranslatorapp.FanOutInput{
		ProjectID: uuid.New(), KeyID: uuid.New(), KeyName: "k",
		SourceLocaleID: sourceID, SourceLocale: "de", SourceValue: "Hallo",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(tx.calls) != 0 {
		t.Fatalf("expected 0 calls, got %d", len(tx.calls))
	}
}

func TestFanOutNoEnabledProvidersIsNoOp(t *testing.T) {
	provs := &stubProviderRepo{enabled: nil}
	locs := &stubLocaleRepo{}
	trs := &stubTranslationRepo{existing: map[uuid.UUID]translation.Translation{}}
	auds := &stubAuditRepo{}
	tx := &stubTranslator{}
	uc := aitranslatorapp.New(provs, locs, trs, auds, tx, stubSealer{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := uc.Execute(context.Background(), aitranslatorapp.FanOutInput{
		SourceLocaleID: uuid.New(),
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(tx.calls) != 0 || len(trs.upserted) != 0 || len(auds.entries) != 0 {
		t.Fatal("expected fully silent no-op")
	}
}

func TestFanOutRequiresSealer(t *testing.T) {
	uc := aitranslatorapp.New(&stubProviderRepo{}, &stubLocaleRepo{}, &stubTranslationRepo{}, &stubAuditRepo{}, &stubTranslator{}, nil, nil, nil)
	err := uc.Execute(context.Background(), aitranslatorapp.FanOutInput{})
	if err == nil || !errors.Is(err, err) {
		t.Fatal("expected error when sealer nil")
	}
}
