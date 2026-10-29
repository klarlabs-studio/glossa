package httpgin

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go.klarlabs.de/glossa/apierr/ginerr"
	projectapp "go.klarlabs.de/glossa/apps/api/internal/app/project"
	"go.klarlabs.de/glossa/apps/api/internal/domain/locale"
	"go.klarlabs.de/glossa/apps/api/internal/domain/project"
	"go.klarlabs.de/glossa/apps/api/internal/errs"
)

type createProjectReq struct {
	TenantID      string `json:"tenantId" binding:"required"`
	Slug          string `json:"slug" binding:"required"`
	Name          string `json:"name" binding:"required"`
	DefaultLocale string `json:"defaultLocale"`
}

type createProjectRes struct {
	ID            string `json:"id"`
	TenantID      string `json:"tenantId"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	DefaultLocale string `json:"defaultLocale"`
	// APIKey is the cleartext key — surfaced exactly once at create
	// time. After this response the server only knows the SHA-256
	// hash. Callers MUST show this to the human and then discard it.
	APIKey string `json:"apiKey"`
}

func handleCreateProject(uc *projectapp.CreateProject) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req createProjectReq
		if err := c.ShouldBindJSON(&req); err != nil {
			ginerr.Send(c, errs.BadRequestFromErr(err))
			return
		}
		tenantID, err := uuid.Parse(req.TenantID)
		if err != nil {
			ginerr.Send(c, errs.ValidationTenantIDNotUUID)
			return
		}
		out, err := uc.Execute(c.Request.Context(), projectapp.CreateInput{
			TenantID:      tenantID,
			Slug:          req.Slug,
			Name:          req.Name,
			DefaultLocale: req.DefaultLocale,
		})
		if err != nil {
			switch {
			case errors.Is(err, project.ErrInvalidSlug),
				errors.Is(err, project.ErrInvalidName),
				errors.Is(err, locale.ErrInvalidCode),
				errors.Is(err, projectapp.ErrInvalidTenantID):
				ginerr.Send(c, errs.UnprocessableFromErr(err))
			default:
				ginerr.Send(c, errs.InternalFromErr(err))
			}
			return
		}
		c.JSON(http.StatusCreated, createProjectRes{
			ID:            out.Project.ID.String(),
			TenantID:      out.Project.TenantID.String(),
			Slug:          out.Project.Slug.String(),
			Name:          out.Project.Name.String(),
			DefaultLocale: out.Project.DefaultLocale,
			APIKey:        out.APIKeyRaw,
		})
	}
}
