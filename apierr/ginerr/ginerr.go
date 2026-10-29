// Package ginerr is the gin-framework adapter for apierr. Two helpers:
//
//	ginerr.Send(c, errs.ValidationEmailRequired.WithParam("field", "email"))
//	ginerr.SendErr(c, err) // unwraps to apierr.Error or wraps as 500
//
// Both terminate the request via c.AbortWithStatusJSON so handler
// code keeps a single-line error path.
package ginerr

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"go.klarlabs.de/glossa/apierr"
)

// Send writes the wire envelope and aborts the request. Use directly
// with an apierr.Error from the package's registry (optionally
// decorated via WithParam / WithMessage).
func Send(c *gin.Context, e *apierr.Error) {
	if e == nil {
		// Defensive default — a nil typed error here is a caller bug;
		// emit a generic 500 rather than serialize "null".
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": apierr.New("internal_error", "errors.internal", "Internal server error", 500),
		})
		return
	}
	c.AbortWithStatusJSON(e.Status, e.Body())
}

// SendErr is the catch-all: unwraps to *apierr.Error via errors.As if
// possible, otherwise wraps the unknown err as a generic 500 with the
// underlying message preserved for logs. Use at the top of handler
// chains where you may catch errors from deeper layers that don't
// know about apierr.
func SendErr(c *gin.Context, err error) {
	if err == nil {
		return
	}
	var typed *apierr.Error
	if errors.As(err, &typed) {
		Send(c, typed)
		return
	}
	wrapped := apierr.New("internal_error", "errors.internal",
		"Internal server error", http.StatusInternalServerError).Wrap(err)
	Send(c, wrapped)
}
