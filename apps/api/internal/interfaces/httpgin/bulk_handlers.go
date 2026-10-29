package httpgin

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go.klarlabs.de/glossa/apierr/ginerr"
	aitranslatorapp "go.klarlabs.de/glossa/apps/api/internal/app/aitranslator"
	translationapp "go.klarlabs.de/glossa/apps/api/internal/app/translation"
	keyapp "go.klarlabs.de/glossa/apps/api/internal/app/translationkey"
	"go.klarlabs.de/glossa/apps/api/internal/domain/analytics"
	"go.klarlabs.de/glossa/apps/api/internal/domain/audit"
	"go.klarlabs.de/glossa/apps/api/internal/domain/locale"
	"go.klarlabs.de/glossa/apps/api/internal/domain/project"
	"go.klarlabs.de/glossa/apps/api/internal/domain/translation"
	"go.klarlabs.de/glossa/apps/api/internal/errs"
)

// handleBulkImport — POST /api/v1/admin/projects/:slug/locales/:locale/bulk
//
// Body: { "messages": { "key": "value", ... }, "status": "approved" }
// Upserts every (key, locale) pair atomically, creating missing
// keys on the fly. Used by the admin UI's import flow and by
// translators paste-importing a fully edited bundle.
func handleBulkImport(
	projects project.Repository,
	locales locale.Repository,
	keysUC *keyapp.UpsertKeys,
	keysFind keysFinder,
	uc *translationapp.UpdateTranslation,
	translations translation.Repository,
	pub translationapp.Publisher,
	audits audit.Repository,
	fanOut *aitranslatorapp.FanOut,
	rec analytics.Recorder,
) gin.HandlerFunc {
	type req struct {
		Messages map[string]string `json:"messages" binding:"required"`
		Status   string            `json:"status"`
	}
	return func(c *gin.Context) {
		p, err := resolveProject(c, projects)
		if err != nil {
			ginerr.Send(c, errs.ProjectNotFound)
			return
		}
		localeCode := c.Param("locale")
		var body req
		if err := c.ShouldBindJSON(&body); err != nil {
			ginerr.Send(c, errs.BadRequestFromErr(err))
			return
		}
		status := body.Status
		if status == "" {
			status = string(translation.StatusNeedsReview)
		}
		// Resolve locale.
		allLocales, err := locales.ListForProject(contextOf(c), p.ID)
		if err != nil {
			ginerr.Send(c, errs.InternalFromErr(err))
			return
		}
		l, ok := findLocale(allLocales, localeCode)
		if !ok {
			ginerr.Send(c, errs.LocaleNotFound)
			return
		}
		localeCode = l.Code.String()

		// Ensure every key exists. UpsertKeys is idempotent.
		inputs := make([]keyapp.UpsertInput, 0, len(body.Messages))
		for name := range body.Messages {
			inputs = append(inputs, keyapp.UpsertInput{Name: name})
		}
		if _, err := keysUC.Execute(contextOf(c), p.ID, inputs); err != nil {
			ginerr.Send(c, errs.UnprocessableFromErr(err))
			return
		}

		actor := uuid.Nil
		if v, ok := c.Get(ctxKeyUserID); ok {
			actor, _ = v.(uuid.UUID)
		}
		tenantID, _ := c.Get(ctxKeyTenantID)

		// Apply translations one by one. Per-row failures are
		// reported alongside successes; the request completes either
		// way so a single bad key doesn't strand the rest.
		results := make([]gin.H, 0, len(body.Messages))
		failed := 0
		for name, value := range body.Messages {
			keyID, err := keysFind.FindByName(contextOf(c), p.ID, name)
			if err != nil {
				failed++
				results = append(results, gin.H{"key": name, "error": err.Error()})
				continue
			}
			beforeValue := ""
			if prev, perr := translations.Find(contextOf(c), keyID, l.ID); perr == nil {
				beforeValue = prev.Value
			} else if !errors.Is(perr, translation.ErrNotFound) {
				failed++
				results = append(results, gin.H{"key": name, "error": perr.Error()})
				continue
			}
			out, err := uc.Execute(contextOf(c), translationapp.UpdateInput{
				KeyID:     keyID,
				LocaleID:  l.ID,
				Value:     value,
				Status:    status,
				UpdatedBy: actor,
			})
			if err != nil {
				failed++
				results = append(results, gin.H{"key": name, "error": err.Error()})
				continue
			}
			_ = audits.Append(contextOf(c), audit.Entry{
				TenantID:      tenantID.(uuid.UUID),
				TranslationID: out.ID,
				BeforeValue:   beforeValue,
				AfterValue:    out.Value,
				ChangedBy:     actor,
			})
			pub.Publish(p.ID, tenantID.(uuid.UUID), translationapp.Event{
				Type:    "translation.updated",
				Project: p.Slug.String(),
				Locale:  localeCode,
				Key:     name,
				Value:   out.Value,
				Status:  string(out.Status),
			})
			if fanOut != nil && l.Code.Matches(p.DefaultLocale) {
				fanOut.Trigger(aitranslatorapp.FanOutInput{
					TenantID:       tenantID.(uuid.UUID),
					ProjectID:      p.ID,
					KeyID:          keyID,
					KeyName:        name,
					SourceLocaleID: l.ID,
					SourceLocale:   localeCode,
					SourceValue:    out.Value,
				})
			}
			results = append(results, gin.H{"key": name, "id": out.ID.String()})
		}
		applied := len(body.Messages) - failed
		if rec != nil && applied > 0 {
			pid := p.ID
			tID, _ := tenantID.(uuid.UUID)
			// Bulk import covers both first-key-sync (when the bundle
			// includes keys we hadn't seen) and translation_edited
			// activity. Emit both; first-time variants are derived at
			// read time via MIN(occurred_at).
			_ = rec.Record(contextOf(c), analytics.Event{
				TenantID:  tID,
				ProjectID: &pid,
				Kind:      analytics.KindKeySynced,
				Metadata:  map[string]any{"count": applied, "source": "bulk"},
			})
			_ = rec.Record(contextOf(c), analytics.Event{
				TenantID:  tID,
				ProjectID: &pid,
				Kind:      analytics.KindTranslationEdited,
				Metadata:  map[string]any{"count": applied, "locale": localeCode, "source": "bulk"},
			})
		}

		code := http.StatusOK
		if failed > 0 && failed == len(body.Messages) {
			code = http.StatusUnprocessableEntity
		}
		c.JSON(code, gin.H{
			"applied": len(body.Messages) - failed,
			"failed":  failed,
			"results": results,
		})
	}
}

// handleBundleDiff — GET /api/v1/admin/projects/:slug/diff
//
// Returns per-locale counts of untranslated / needs-review /
// approved keys so the admin diff view can render a snapshot at a
// glance. No drill-down; the admin SPA hits the bundle endpoint
// per locale for the per-key list.
func handleBundleDiff(
	projects project.Repository,
	locales locale.Repository,
	listBundle *translationapp.ListBundle,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, err := resolveProject(c, projects)
		if err != nil {
			ginerr.Send(c, errs.ProjectNotFound)
			return
		}
		all, err := locales.ListForProject(contextOf(c), p.ID)
		if err != nil {
			ginerr.Send(c, errs.InternalFromErr(err))
			return
		}
		out := make([]gin.H, 0, len(all))
		for _, l := range all {
			entries, err := listBundle.Execute(contextOf(c), p.ID, l.ID)
			if err != nil {
				ginerr.Send(c, errs.InternalFromErr(err))
				return
			}
			pending, aiTranslated, needsReview, approved := 0, 0, 0, 0
			for _, e := range entries {
				switch e.Status {
				case translation.StatusApproved:
					approved++
				case translation.StatusNeedsReview:
					needsReview++
				case translation.StatusAITranslated:
					aiTranslated++
				default:
					pending++
				}
			}
			out = append(out, gin.H{
				"locale":       l.Code.String(),
				"label":        l.Label.String(),
				"total":        len(entries),
				"pending":      pending,
				"aiTranslated": aiTranslated,
				"needsReview":  needsReview,
				"approved":     approved,
			})
		}
		c.JSON(http.StatusOK, gin.H{"project": p.Slug.String(), "locales": out})
	}
}
