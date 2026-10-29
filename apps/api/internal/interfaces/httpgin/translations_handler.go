package httpgin

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go.klarlabs.de/glossa/apierr/ginerr"
	aitranslatorapp "go.klarlabs.de/glossa/apps/api/internal/app/aitranslator"
	translationapp "go.klarlabs.de/glossa/apps/api/internal/app/translation"
	"go.klarlabs.de/glossa/apps/api/internal/domain/analytics"
	"go.klarlabs.de/glossa/apps/api/internal/domain/audit"
	"go.klarlabs.de/glossa/apps/api/internal/domain/locale"
	"go.klarlabs.de/glossa/apps/api/internal/domain/project"
	"go.klarlabs.de/glossa/apps/api/internal/domain/translation"
	"go.klarlabs.de/glossa/apps/api/internal/domain/user"
	"go.klarlabs.de/glossa/apps/api/internal/errs"
)

// handleListBundle returns the full (project, locale) message map.
// Used by the SDK at runtime, the CLI at build time, and the admin
// UI to populate the key list.
func handleListBundle(
	uc *translationapp.ListBundle,
	projects project.Repository,
	locales locale.Repository,
	rec analytics.Recorder,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, err := resolveProject(c, projects)
		if err != nil {
			ginerr.Send(c, errs.ProjectNotFound)
			return
		}
		code := c.Param("locale")
		all, err := locales.ListForProject(contextOf(c), p.ID)
		if err != nil {
			ginerr.Send(c, errs.InternalFromErr(err))
			return
		}
		found, ok := findLocale(all, code)
		if !ok {
			ginerr.Send(c, errs.LocaleNotFoundForProject)
			return
		}
		entries, err := uc.Execute(contextOf(c), p.ID, found.ID)
		if err != nil {
			ginerr.Send(c, errs.InternalFromErr(err))
			return
		}
		messages := make(map[string]string, len(entries))
		statuses := make(map[string]string, len(entries))
		for _, e := range entries {
			messages[e.Key] = e.Value
			if e.Status != "" {
				statuses[e.Key] = string(e.Status)
			}
		}
		// Emit a consumer_request event only when the caller is an
		// API-key holder (no JWT user). Admin browsing the editor
		// shouldn't bump the public-traffic funnel.
		if _, isUser := c.Get(ctxKeyUserID); !isUser && rec != nil {
			pid := p.ID
			tid, _ := c.Get(ctxKeyTenantID)
			tenantID, _ := tid.(uuid.UUID)
			_ = rec.Record(contextOf(c), analytics.Event{
				TenantID:  tenantID,
				ProjectID: &pid,
				Kind:      analytics.KindConsumerRequest,
				Metadata:  map[string]any{"locale": found.Code.String()},
			})
		}

		// The canonical code, whatever spelling was requested, so
		// clients can key caches on it and match SSE events.
		c.JSON(http.StatusOK, gin.H{
			"project":  p.Slug.String(),
			"locale":   found.Code.String(),
			"messages": messages,
			"statuses": statuses,
		})
	}
}

type patchTranslationReq struct {
	Value     string `json:"value" binding:"required"`
	Status    string `json:"status"`
	UpdatedBy string `json:"updatedBy"`
}

// handlePatchTranslation is the translator-edit endpoint.
// PATCH /api/v1/projects/:slug/locales/:locale/keys/:key
//
// Supports both auth flows. Translators (JWT path) are scoped to
// the locales listed on their user record; an out-of-scope edit
// returns 403. The change fans out via SSE and lands in the audit
// log under the resolved tenant.
func handlePatchTranslation(
	uc *translationapp.UpdateTranslation,
	translations translation.Repository,
	projects project.Repository,
	locales locale.Repository,
	keys keysFinder,
	pub translationapp.Publisher,
	audits audit.Repository,
	fanOut *aitranslatorapp.FanOut,
	rec analytics.Recorder,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, err := resolveProject(c, projects)
		if err != nil {
			ginerr.Send(c, errs.ProjectNotFound)
			return
		}
		localeCode := c.Param("locale")
		keyName := c.Param("key")

		// Translator locale scoping. API-key callers carry no role
		// so they're treated as service-level and skip this check.
		if role, ok := c.Get(ctxKeyUserRole); ok && role == string(user.RoleTranslator) {
			if !localeInScope(authedUserLocales(c), localeCode) {
				ginerr.Send(c, errs.TranslatorOutOfScopeLocale)
				return
			}
		}

		var req patchTranslationReq
		if err := c.ShouldBindJSON(&req); err != nil {
			ginerr.Send(c, errs.BadRequestFromErr(err))
			return
		}

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
		// From here on the canonical code, so SSE events, audit rows
		// and the source-locale check never see "de-de" for "de-DE".
		localeCode = l.Code.String()

		k, err := keys.FindByName(contextOf(c), p.ID, keyName)
		if err != nil {
			ginerr.Send(c, errs.KeyNotFoundForProject)
			return
		}

		// Audit actor: JWT path supplies it directly; API-key path
		// optionally reads it from the body; otherwise zero (CLI /
		// system change).
		actor := uuid.Nil
		if v, ok := c.Get(ctxKeyUserID); ok {
			actor, _ = v.(uuid.UUID)
		} else if req.UpdatedBy != "" {
			actor, _ = uuid.Parse(req.UpdatedBy)
		}

		// Read the existing translation (if any) for the audit log
		// before the upsert overwrites it. A first-time write hits
		// translation.ErrNotFound — that's a valid before-state
		// ("nothing"), so the audit row records an empty BeforeValue.
		beforeValue := ""
		if prev, err := translations.Find(contextOf(c), k, l.ID); err == nil {
			beforeValue = prev.Value
		} else if !errors.Is(err, translation.ErrNotFound) {
			ginerr.Send(c, errs.InternalFromErr(err))
			return
		}

		out, err := uc.Execute(contextOf(c), translationapp.UpdateInput{
			KeyID:     k,
			LocaleID:  l.ID,
			Value:     req.Value,
			Status:    req.Status,
			UpdatedBy: actor,
		})
		if err != nil {
			ginerr.Send(c, errs.UnprocessableFromErr(err))
			return
		}

		tenantID, _ := c.Get(ctxKeyTenantID)
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
			Key:     keyName,
			Value:   out.Value,
			Status:  string(out.Status),
		})

		if rec != nil {
			pid := p.ID
			_ = rec.Record(contextOf(c), analytics.Event{
				TenantID:  tenantID.(uuid.UUID),
				ProjectID: &pid,
				Kind:      analytics.KindTranslationEdited,
				Metadata: map[string]any{
					"locale": localeCode,
					"key":    keyName,
					"status": string(out.Status),
				},
			})
		}

		// Source-locale write → fan out AI translations to every other
		// enabled locale that doesn't already have a reviewer-touched
		// entry. Fire-and-forget; the response returns immediately.
		if fanOut != nil && l.Code.Matches(p.DefaultLocale) {
			fanOut.Trigger(aitranslatorapp.FanOutInput{
				TenantID:       tenantID.(uuid.UUID),
				ProjectID:      p.ID,
				KeyID:          k,
				KeyName:        keyName,
				SourceLocaleID: l.ID,
				SourceLocale:   localeCode,
				SourceValue:    out.Value,
			})
		}

		c.JSON(http.StatusOK, gin.H{
			"id":     out.ID.String(),
			"value":  out.Value,
			"status": string(out.Status),
		})
	}
}

// keysFinder resolves a (project, key name) pair to a key UUID.
// Implementation in main.go bridges to translationkey.Repository.
type keysFinder interface {
	FindByName(ctx context.Context, projectID uuid.UUID, name string) (uuid.UUID, error)
}
