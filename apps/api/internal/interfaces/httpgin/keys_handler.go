package httpgin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go.klarlabs.de/glossa/apierr/ginerr"
	keyapp "go.klarlabs.de/glossa/apps/api/internal/app/translationkey"
	"go.klarlabs.de/glossa/apps/api/internal/domain/analytics"
	"go.klarlabs.de/glossa/apps/api/internal/domain/project"
	"go.klarlabs.de/glossa/apps/api/internal/errs"
)

type scanKeysReq struct {
	Keys []keyScanRow `json:"keys" binding:"required"`
}

type keyScanRow struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
}

// handleScanKeys is the batch UPSERT endpoint the CLI's `glossa scan`
// command targets. Per-row errors are returned alongside per-row
// successes so the CLI can map them back to source locations.
func handleScanKeys(projects project.Repository, uc *keyapp.UpsertKeys, rec analytics.Recorder) gin.HandlerFunc {
	return func(c *gin.Context) {
		p, err := resolveProject(c, projects)
		if err != nil {
			ginerr.Send(c, errs.ProjectNotFound)
			return
		}
		var req scanKeysReq
		if err := c.ShouldBindJSON(&req); err != nil {
			ginerr.Send(c, errs.BadRequestFromErr(err))
			return
		}
		inputs := make([]keyapp.UpsertInput, len(req.Keys))
		for i, r := range req.Keys {
			inputs[i] = keyapp.UpsertInput{Name: r.Name, Description: r.Description}
		}
		results, err := uc.Execute(c.Request.Context(), p.ID, inputs)
		if err != nil {
			ginerr.Send(c, errs.UnprocessableFromErr(err))
			return
		}
		out := make([]gin.H, 0, len(results))
		okCount := 0
		for _, r := range results {
			row := gin.H{"name": r.Input.Name}
			if r.Err != nil {
				row["error"] = r.Err.Error()
			} else {
				row["id"] = r.Key.ID.String()
				row["description"] = r.Key.Description
				okCount++
			}
			out = append(out, row)
		}
		if rec != nil && okCount > 0 {
			pid := p.ID
			tid, _ := c.Get(ctxKeyTenantID)
			tenantID, _ := tid.(uuid.UUID)
			_ = rec.Record(c.Request.Context(), analytics.Event{
				TenantID:  tenantID,
				ProjectID: &pid,
				Kind:      analytics.KindKeySynced,
				Metadata:  map[string]any{"count": okCount},
			})
		}
		c.JSON(http.StatusOK, gin.H{"results": out})
	}
}
