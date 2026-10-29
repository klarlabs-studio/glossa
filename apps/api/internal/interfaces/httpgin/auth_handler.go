package httpgin

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go.klarlabs.de/glossa/apierr/ginerr"
	authapp "go.klarlabs.de/glossa/apps/api/internal/app/auth"
	"go.klarlabs.de/glossa/apps/api/internal/domain/tenant"
	"go.klarlabs.de/glossa/apps/api/internal/errs"
)

type discoverReq struct {
	Email string `json:"email" binding:"required"`
}

// handleDiscoverTenants — POST /api/v1/auth/discover.
//
// Two-step login UX: the SPA prompts for email + password, calls
// this endpoint, and decides whether to auto-pick the tenant
// (single match), show a tenant picker (multiple), or surface
// "no account" (empty). Never differentiates "email unknown"
// from "email present with zero memberships" — both return `[]`
// to deny user-enumeration probes. Same per-IP rate limit as
// /auth/login.
func handleDiscoverTenants(uc *authapp.DiscoverTenants) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req discoverReq
		if err := c.ShouldBindJSON(&req); err != nil {
			ginerr.Send(c, errs.BadRequestFromErr(err))
			return
		}
		opts, err := uc.Execute(c.Request.Context(), req.Email)
		if err != nil {
			ginerr.Send(c, errs.InternalFromErr(err))
			return
		}
		c.JSON(http.StatusOK, gin.H{"tenants": opts})
	}
}

type loginReq struct {
	TenantSlug string `json:"tenantSlug" binding:"required"`
	Email      string `json:"email" binding:"required"`
	Password   string `json:"password" binding:"required"`
}

// handleLogin is the JWT issuance endpoint. POST /api/v1/auth/login.
// Translator clients hit it once per session; the admin SPA
// persists the returned token to localStorage.
//
// Tenant resolution: callers supply the tenant slug. We avoid
// host-based tenant inference for now because single-domain
// deploys (the default Glossa shape) need the explicit pick.
func handleLogin(uc *authapp.Login, tenants tenant.Repository) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req loginReq
		if err := c.ShouldBindJSON(&req); err != nil {
			ginerr.Send(c, errs.BadRequestFromErr(err))
			return
		}
		slug, err := tenant.NewSlug(req.TenantSlug)
		if err != nil {
			ginerr.Send(c, errs.AuthInvalidCredentials)
			return
		}
		t, err := tenants.FindBySlug(c.Request.Context(), slug)
		if err != nil {
			ginerr.Send(c, errs.AuthInvalidCredentials)
			return
		}
		out, err := uc.Execute(c.Request.Context(), authapp.LoginInput{
			TenantID: t.ID,
			Email:    req.Email,
			Password: req.Password,
		})
		if err != nil {
			if errors.Is(err, authapp.ErrInvalidCredentials) {
				ginerr.Send(c, errs.AuthInvalidCredentials)
				return
			}
			ginerr.Send(c, errs.InternalFromErr(err))
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"token":   out.Token,
			"expires": out.Expires,
			"user": gin.H{
				"id":      out.User.ID.String(),
				"email":   out.User.Email,
				"role":    string(out.User.Role),
				"locales": out.User.Locales,
			},
			"tenant": gin.H{
				"id":   t.ID.String(),
				"slug": t.Slug.String(),
				"name": t.Name.String(),
			},
		})
	}
}

// handleMe returns the JWT-resolved current user. The admin SPA
// calls this on boot to verify the stored token before showing
// the editor.
func handleMe() gin.HandlerFunc {
	return func(c *gin.Context) {
		uid := authedUserID(c)
		role, _ := c.Get(ctxKeyUserRole)
		email, _ := c.Get(ctxKeyUserEmail)
		tenantID, _ := c.Get(ctxKeyTenantID)
		c.JSON(http.StatusOK, gin.H{
			"id":       uid.String(),
			"email":    email,
			"role":     role,
			"locales":  authedUserLocales(c),
			"tenantId": tenantID.(uuid.UUID).String(),
		})
	}
}
