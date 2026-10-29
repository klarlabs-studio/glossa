package httpgin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go.klarlabs.de/glossa/apierr/ginerr"
	"go.klarlabs.de/glossa/apps/api/internal/domain/analytics"
	"go.klarlabs.de/glossa/apps/api/internal/domain/project"
	"go.klarlabs.de/glossa/apps/api/internal/errs"
)

// handleProjectMetrics — GET /api/v1/admin/projects/:slug/metrics
//
// Returns the funnel for a single project: every event kind we've
// seen, plus its first-occurrence timestamp + total count. The admin
// 'Metrics' tab joins this with the project's createdAt to compute
// time-to-first-X.
func handleProjectMetrics(projects project.Repository, repo analytics.Repository) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, err := resolveProject(c, projects)
		if err != nil {
			ginerr.Send(c, errs.ProjectNotFound)
			return
		}
		rows, err := repo.ProjectFunnel(contextOf(c), p.TenantID, p.ID)
		if err != nil {
			ginerr.Send(c, errs.InternalFromErr(err))
			return
		}
		out := make([]gin.H, 0, len(rows))
		for _, r := range rows {
			out = append(out, gin.H{
				"kind":    string(r.Kind),
				"firstAt": r.FirstAt,
				"total":   r.Total,
			})
		}
		c.JSON(http.StatusOK, gin.H{
			"project": p.Slug.String(),
			"events":  out,
		})
	}
}

// handleTenantMetrics — GET /api/v1/admin/metrics
//
// Tenant-wide cohort: one (projectId, kind, firstAt) row per pair.
// Powers a cross-project funnel like "time-to-first-approved-key
// median across all my projects".
func handleTenantMetrics(repo analytics.Repository) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID, _ := c.Get(ctxKeyTenantID)
		rows, err := repo.TenantProjectsFirstEvents(contextOf(c), tenantID.(uuid.UUID))
		if err != nil {
			ginerr.Send(c, errs.InternalFromErr(err))
			return
		}
		out := make([]gin.H, 0, len(rows))
		for _, r := range rows {
			out = append(out, gin.H{
				"projectId": r.ProjectID.String(),
				"kind":      string(r.Kind),
				"firstAt":   r.FirstAt,
			})
		}
		c.JSON(http.StatusOK, gin.H{"firstEvents": out})
	}
}
