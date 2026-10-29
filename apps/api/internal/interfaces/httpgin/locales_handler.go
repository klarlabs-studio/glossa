package httpgin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go.klarlabs.de/glossa/apierr/ginerr"
	"go.klarlabs.de/glossa/apps/api/internal/domain/locale"
	"go.klarlabs.de/glossa/apps/api/internal/domain/project"
	"go.klarlabs.de/glossa/apps/api/internal/errs"
)

type createLocaleReq struct {
	Code  string `json:"code" binding:"required"`
	Label string `json:"label" binding:"required"`
}

func handleCreateLocale(projects project.Repository, repo locale.Repository) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, err := resolveProject(c, projects)
		if err != nil {
			ginerr.Send(c, errs.ProjectNotFound)
			return
		}
		var req createLocaleReq
		if err := c.ShouldBindJSON(&req); err != nil {
			ginerr.Send(c, errs.BadRequestFromErr(err))
			return
		}
		code, err := locale.NewCode(req.Code)
		if err != nil {
			ginerr.Send(c, errs.UnprocessableFromErr(err))
			return
		}
		label, err := locale.NewLabel(req.Label)
		if err != nil {
			ginerr.Send(c, errs.UnprocessableFromErr(err))
			return
		}
		l := locale.Locale{
			ID:        uuid.New(),
			ProjectID: p.ID,
			Code:      code,
			Label:     label,
			Enabled:   true,
		}
		if err := repo.Save(c.Request.Context(), l); err != nil {
			ginerr.Send(c, errs.InternalFromErr(err))
			return
		}
		c.JSON(http.StatusCreated, localeJSON(l))
	}
}

func handleListLocales(projects project.Repository, repo locale.Repository) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, err := resolveProject(c, projects)
		if err != nil {
			ginerr.Send(c, errs.ProjectNotFound)
			return
		}
		rows, err := repo.ListForProject(c.Request.Context(), p.ID)
		if err != nil {
			ginerr.Send(c, errs.InternalFromErr(err))
			return
		}
		out := make([]gin.H, 0, len(rows))
		for _, l := range rows {
			out = append(out, localeJSON(l))
		}
		c.JSON(http.StatusOK, out)
	}
}

// localeJSON is the wire shape of a locale. Language, script and
// region are the explicit subtags of the canonical code; direction is
// derived from the (likely) script so clients can set dir without
// keeping their own RTL tables.
func localeJSON(l locale.Locale) gin.H {
	return gin.H{
		"id":        l.ID.String(),
		"code":      l.Code.String(),
		"language":  l.Code.Language(),
		"script":    l.Code.Script(),
		"region":    l.Code.Region(),
		"direction": string(l.Code.Direction()),
		"label":     l.Label.String(),
		"enabled":   l.Enabled,
	}
}

// findLocale resolves a locale URL segment against a project's
// locales by canonical BCP 47 form, so "de-de", "de_DE" and "de-DE"
// all name the same row.
func findLocale(all []locale.Locale, raw string) (locale.Locale, bool) {
	for _, l := range all {
		if l.Code.Matches(raw) {
			return l, true
		}
	}
	return locale.Locale{}, false
}

// localeInScope reports whether a translator's locale scopes cover raw,
// comparing canonical forms.
func localeInScope(scopes []string, raw string) bool {
	for _, s := range scopes {
		if locale.Code(s).Matches(raw) {
			return true
		}
	}
	return false
}

// canonicalLocales validates translator locale scopes and stores them
// canonically, de-duplicated in input order.
func canonicalLocales(raw []string) ([]string, error) {
	out := make([]string, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, r := range raw {
		code, err := locale.NewCode(r)
		if err != nil {
			return nil, err
		}
		if !seen[code.String()] {
			seen[code.String()] = true
			out = append(out, code.String())
		}
	}
	return out, nil
}
