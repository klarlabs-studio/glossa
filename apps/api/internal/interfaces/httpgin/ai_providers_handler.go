package httpgin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go.klarlabs.de/glossa/apierr/ginerr"
	"go.klarlabs.de/glossa/apps/api/internal/domain/aitranslator"
	"go.klarlabs.de/glossa/apps/api/internal/errs"
)

type aiProviderListItem struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Label     string    `json:"label"`
	BaseURL   string    `json:"baseUrl"`
	Model     string    `json:"model"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"createdAt"`
}

func handleListAIProviders(repo aitranslator.Repository) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantID, _ := c.Get(ctxKeyTenantID)
		rows, err := repo.List(contextOf(c), tenantID.(uuid.UUID))
		if err != nil {
			ginerr.Send(c, errs.InternalFromErr(err))
			return
		}
		out := make([]aiProviderListItem, 0, len(rows))
		for _, p := range rows {
			out = append(out, aiProviderListItem{
				ID: p.ID.String(), Kind: string(p.Kind), Label: p.Label,
				BaseURL: p.BaseURL, Model: p.Model, Enabled: p.Enabled, CreatedAt: p.CreatedAt,
			})
		}
		c.JSON(http.StatusOK, gin.H{"providers": out})
	}
}

type createAIProviderReq struct {
	Kind    string `json:"kind" binding:"required"`
	Label   string `json:"label" binding:"required"`
	BaseURL string `json:"baseUrl"`
	Model   string `json:"model" binding:"required"`
	APIKey  string `json:"apiKey" binding:"required"`
	Enabled *bool  `json:"enabled"`
}

func handleCreateAIProvider(repo aitranslator.Repository, sealer Sealer, translator aitranslator.Translator) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sealer == nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error": "AI translation disabled: GLOSSA_SECRETS_KEY is not configured",
			})
			return
		}
		var req createAIProviderReq
		if err := c.ShouldBindJSON(&req); err != nil {
			ginerr.Send(c, errs.BadRequestFromErr(err))
			return
		}
		kind, err := aitranslator.ParseKind(req.Kind)
		if err != nil {
			ginerr.Send(c, errs.BadRequestFromErr(err))
			return
		}
		if strings.TrimSpace(req.APIKey) == "" {
			ginerr.Send(c, errs.ValidationAPIKeyRequired)
			return
		}
		// Validate-on-save (default true) catches typo'd keys before
		// they pollute the DB. Skip with ?validate=false for power
		// users who want to plant a row to edit later.
		if c.DefaultQuery("validate", "true") != "false" && translator != nil {
			tenantID, _ := c.Get(ctxKeyTenantID)
			tmp := aitranslator.Provider{
				TenantID: tenantID.(uuid.UUID), Kind: kind, BaseURL: req.BaseURL, Model: req.Model,
			}
			ctx, cancel := context.WithTimeout(contextOf(c), 20*time.Second)
			defer cancel()
			if _, verr := translator.Translate(ctx, tmp, []byte(req.APIKey), aitranslator.TranslateRequest{
				Key: "validate.ping", SourceLocale: "de", TargetLocale: "en", Source: "Hallo",
			}); verr != nil {
				c.AbortWithStatusJSON(http.StatusUnprocessableEntity, gin.H{
					"error": "provider rejected the key: " + verr.Error(),
				})
				return
			}
		}
		ct, nonce, err := sealer.Seal([]byte(req.APIKey))
		if err != nil {
			ginerr.Send(c, errs.InternalFromErr(err))
			return
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		tenantID, _ := c.Get(ctxKeyTenantID)
		row, err := repo.Create(contextOf(c), aitranslator.Provider{
			TenantID:    tenantID.(uuid.UUID),
			Kind:        kind,
			Label:       req.Label,
			BaseURL:     req.BaseURL,
			Model:       req.Model,
			APIKeyCT:    ct,
			APIKeyNonce: nonce,
			Enabled:     enabled,
		})
		if err != nil {
			ginerr.Send(c, errs.UnprocessableFromErr(err))
			return
		}
		c.JSON(http.StatusCreated, aiProviderListItem{
			ID: row.ID.String(), Kind: string(row.Kind), Label: row.Label,
			BaseURL: row.BaseURL, Model: row.Model, Enabled: row.Enabled, CreatedAt: row.CreatedAt,
		})
	}
}

type updateAIProviderReq struct {
	Label   string `json:"label" binding:"required"`
	BaseURL string `json:"baseUrl"`
	Model   string `json:"model" binding:"required"`
	Enabled bool   `json:"enabled"`
	APIKey  string `json:"apiKey"`
}

func handleUpdateAIProvider(repo aitranslator.Repository, sealer Sealer) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			ginerr.Send(c, errs.ValidationInvalidID)
			return
		}
		var req updateAIProviderReq
		if err := c.ShouldBindJSON(&req); err != nil {
			ginerr.Send(c, errs.BadRequestFromErr(err))
			return
		}
		if err := repo.Update(contextOf(c), id, req.Label, req.BaseURL, req.Model, req.Enabled); err != nil {
			ginerr.Send(c, errs.UnprocessableFromErr(err))
			return
		}
		if strings.TrimSpace(req.APIKey) != "" {
			if sealer == nil {
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
					"error": "AI translation disabled: GLOSSA_SECRETS_KEY is not configured",
				})
				return
			}
			ct, nonce, err := sealer.Seal([]byte(req.APIKey))
			if err != nil {
				ginerr.Send(c, errs.InternalFromErr(err))
				return
			}
			if err := repo.UpdateKey(contextOf(c), id, ct, nonce); err != nil {
				ginerr.Send(c, errs.UnprocessableFromErr(err))
				return
			}
		}
		c.Status(http.StatusNoContent)
	}
}

func handleDeleteAIProvider(repo aitranslator.Repository) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			ginerr.Send(c, errs.ValidationInvalidID)
			return
		}
		if err := repo.Delete(contextOf(c), id); err != nil {
			ginerr.Send(c, errs.InternalFromErr(err))
			return
		}
		c.Status(http.StatusNoContent)
	}
}

type backfillReq struct {
	Source string `json:"source" binding:"required"`
}

// handleAITestProvider does a one-shot translate call against a single
// provider so the admin UI's "Test" button can surface auth/model
// errors before the user trusts the row.
func handleAITestProvider(
	repo aitranslator.Repository,
	sealer Sealer,
	translator aitranslator.Translator,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		if sealer == nil {
			ginerr.Send(c, errs.AITranslationDisabled)
			return
		}
		id, err := uuid.Parse(c.Param("id"))
		if err != nil {
			ginerr.Send(c, errs.ValidationInvalidID)
			return
		}
		var req backfillReq
		if err := c.ShouldBindJSON(&req); err != nil {
			ginerr.Send(c, errs.BadRequestFromErr(err))
			return
		}
		prov, err := repo.Get(contextOf(c), id)
		if err != nil {
			if errors.Is(err, aitranslator.ErrNotFound) {
				ginerr.Send(c, errs.AIProviderNotFound)
				return
			}
			ginerr.Send(c, errs.InternalFromErr(err))
			return
		}
		key, err := sealer.Open(prov.APIKeyCT, prov.APIKeyNonce)
		if err != nil {
			ginerr.Send(c, errs.InternalFromErr(err))
			return
		}
		ctx, cancel := context.WithTimeout(contextOf(c), 20*time.Second)
		defer cancel()
		res, err := translator.Translate(ctx, prov, key, aitranslator.TranslateRequest{
			Key:          "test.ping",
			SourceLocale: "de",
			TargetLocale: "en",
			Source:       req.Source,
		})
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"ok": false, "error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true, "translation": res.Translation, "provider": res.Provider})
	}
}
