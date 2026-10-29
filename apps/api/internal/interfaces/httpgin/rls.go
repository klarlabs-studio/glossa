package httpgin

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"go.klarlabs.de/glossa/apierr/ginerr"
	"go.klarlabs.de/glossa/apps/api/internal/db"
	"go.klarlabs.de/glossa/apps/api/internal/errs"
)

// rlsTxMiddleware enforces Postgres row-level-security tenant
// isolation per request. After apiKeyAuth has resolved the tenant,
// this middleware opens a transaction, runs
// `SET LOCAL app.current_tenant = '<uuid>'`, and binds a tx-scoped
// [db.Queries] onto the request context. Every repository call in the
// handler chain picks that Queries up via [db.QueriesFromContext], so
// the RLS policies declared in 0001_init.up.sql actually fire.
//
// Order matters: this must be registered AFTER apiKeyAuth.
//
// The transaction is committed on 2xx/3xx responses and rolled back
// on 4xx/5xx so a failed write does not leak through. SET LOCAL is
// tx-scoped, so the connection returns to the pool with no leftover
// session state.
func rlsTxMiddleware(pool *pgxpool.Pool) gin.HandlerFunc {
	return func(c *gin.Context) {
		tenantRaw, ok := c.Get(ctxKeyTenantID)
		if !ok {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "rls: tenant not set on context (middleware ordering bug)",
			})
			return
		}
		tenantID, ok := tenantRaw.(uuid.UUID)
		if !ok {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error": "rls: tenant context value has wrong type",
			})
			return
		}

		ctx := c.Request.Context()
		tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			ginerr.Send(c, errs.InternalFromErr(err))
			return
		}
		// Defer rollback as a safety net. Commit on success below
		// supersedes it; Postgres treats a second rollback as a no-op
		// here because the tx has already finalised.
		defer func() { _ = tx.Rollback(ctx) }()

		// SET LOCAL takes the value as text; parametrising with $1
		// works for set_config() but not SET LOCAL. Format the UUID
		// directly — uuid.UUID.String() is hex-only, no escaping
		// concerns.
		if _, err := tx.Exec(ctx, "SET LOCAL app.current_tenant = '"+tenantID.String()+"'"); err != nil {
			ginerr.Send(c, errs.InternalFromErr(err))
			return
		}

		q := db.New(tx)
		c.Request = c.Request.WithContext(db.WithQueries(ctx, q))

		c.Next()

		if c.Writer.Status() >= 400 {
			return // deferred rollback wins
		}
		if err := tx.Commit(ctx); err != nil {
			// Headers already flushed by handlers; log via gin's
			// context but we can't mutate the response any more.
			_ = c.Error(err)
		}
	}
}
