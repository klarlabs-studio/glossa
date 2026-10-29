package httpgin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"go.klarlabs.de/glossa/apierr/ginerr"
	"go.klarlabs.de/glossa/apps/api/internal/app/auth"
	"go.klarlabs.de/glossa/apps/api/internal/domain/user"
	"go.klarlabs.de/glossa/apps/api/internal/errs"
)

// Gin context keys populated by [jwtAuth].
const (
	ctxKeyUserID      = "glossa.user_id"
	ctxKeyUserRole    = "glossa.user_role"
	ctxKeyUserEmail   = "glossa.user_email"
	ctxKeyUserLocales = "glossa.user_locales"
	ctxKeyTokenClaims = "glossa.token_claims"
)

// jwtAuth parses a Bearer JWT and exposes the verified claims on
// the gin context. Sets ctxKeyTenantID so the existing
// [rlsTxMiddleware] picks it up identically to the API-key flow —
// admin routes share the same per-request RLS plumbing.
//
// Failures all return 401 with a single opaque message to deny
// status-code probing.
func jwtAuth(iss auth.TokenIssuer) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := bearerToken(c.GetHeader("Authorization"))
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error": "missing or malformed Authorization header (expected: Bearer <jwt>)",
			})
			return
		}
		claims, err := iss.Verify(raw)
		if err != nil {
			ginerr.Send(c, errs.AuthInvalidToken)
			return
		}
		c.Set(ctxKeyUserID, claims.UserID)
		c.Set(ctxKeyTenantID, claims.TenantID)
		c.Set(ctxKeyUserRole, string(claims.Role))
		c.Set(ctxKeyUserEmail, claims.Email)
		c.Set(ctxKeyUserLocales, claims.Locales)
		c.Set(ctxKeyTokenClaims, claims)
		c.Next()
	}
}

// requireAdmin gates a handler chain to admin users. Translators
// get 403 — useful distinction from 401 because the credential is
// valid, the role just isn't enough.
func requireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		role, _ := c.Get(ctxKeyUserRole)
		if role != string(user.RoleAdmin) {
			ginerr.Send(c, errs.AuthAdminRequired)
			return
		}
		c.Next()
	}
}

// authedUserID returns the user UUID set by [jwtAuth]. Panics if
// called outside the chain — wiring bug.
func authedUserID(c *gin.Context) uuid.UUID {
	v, ok := c.Get(ctxKeyUserID)
	if !ok {
		panic("authedUserID called without jwtAuth middleware")
	}
	return v.(uuid.UUID)
}

// authedUserLocales returns the locales array stamped into the JWT
// at issue time. Empty slice means "no scope" for a translator
// (which is the safe default), or "all locales" for an admin —
// the role check in handlers disambiguates.
func authedUserLocales(c *gin.Context) []string {
	v, _ := c.Get(ctxKeyUserLocales)
	out, _ := v.([]string)
	return out
}
