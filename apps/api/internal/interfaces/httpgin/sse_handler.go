package httpgin

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	translationapp "go.klarlabs.de/glossa/apps/api/internal/app/translation"
)

// defaultHeartbeat is how often a comment frame is written to keep
// corporate proxies (Squid, F5, etc.) from idling out the
// connection. Spec calls for 30s; tests override via the handler
// option to keep them fast.
const defaultHeartbeat = 30 * time.Second

// handleSSE streams translation.updated events for the authed
// project over Server-Sent Events.
//
// Per docs/design.md §5.1: `GET /api/v1/projects/:slug/sse`.
//
// Reconnection: the browser EventSource automatically sets the
// `Last-Event-ID` header when reopening; we parse it and ask the
// hub to replay any events whose monotonic ID is greater than the
// supplied one. If the gap is larger than the hub's history depth,
// the client misses events — that's a feature: it forces an SDK
// re-bundle rather than papering over the gap.
func handleSSE(hub *translationapp.Hub, heartbeat time.Duration) gin.HandlerFunc {
	if heartbeat <= 0 {
		heartbeat = defaultHeartbeat
	}
	return func(c *gin.Context) {
		p := authedProject(c)

		// SSE response headers. Disabling buffering matters behind
		// nginx, which otherwise queues events until the buffer
		// fills.
		c.Writer.Header().Set("Content-Type", "text/event-stream")
		c.Writer.Header().Set("Cache-Control", "no-cache")
		c.Writer.Header().Set("Connection", "keep-alive")
		c.Writer.Header().Set("X-Accel-Buffering", "no")

		lastEventID := uint64(0)
		if h := c.GetHeader("Last-Event-ID"); h != "" {
			if v, err := strconv.ParseUint(h, 10, 64); err == nil {
				lastEventID = v
			}
		}

		sub := hub.Subscribe(p.ID, lastEventID)
		defer hub.Unsubscribe(p.ID, sub)

		// Flush whatever response headers gin has buffered so the
		// client sees the connection open immediately. Without
		// this, an EventSource doesn't fire `onopen` until the
		// first event lands — confusing on a long quiet period.
		c.Writer.Flush()

		ticker := time.NewTicker(heartbeat)
		defer ticker.Stop()

		clientGone := c.Request.Context().Done()

		c.Stream(func(w io.Writer) bool {
			select {
			case <-clientGone:
				return false
			case e, ok := <-sub.C:
				if !ok {
					// Hub dropped us (slow consumer). Close the
					// stream — the client will reconnect with
					// Last-Event-ID and resume from history.
					return false
				}
				if err := writeEvent(w, e); err != nil {
					return false
				}
				return true
			case <-ticker.C:
				// SSE comment line — ignored by EventSource but
				// keeps the TCP path warm.
				if _, err := io.WriteString(w, ": heartbeat\n\n"); err != nil {
					return false
				}
				return true
			}
		})
	}
}

// writeEvent renders one Event in the SSE wire format:
//
//	id: <monotonic id>
//	event: translation.updated
//	data: <json>
//	<blank line>
//
// Returns the first write error encountered; the caller closes the
// stream on error.
func writeEvent(w io.Writer, e translationapp.Event) error {
	payload, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %d\n", e.ID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\n", e.Type); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
		return err
	}
	return nil
}
