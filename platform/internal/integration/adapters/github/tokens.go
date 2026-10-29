package github

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"go.klarlabs.de/glossa/platform/internal/integration/app"
)

// tokenRefreshMargin: a cached installation token is replaced this long
// before it expires, so no call starts with a token about to lapse.
const tokenRefreshMargin = 5 * time.Minute

type cachedToken struct {
	value   string
	expires time.Time
}

// tokenCache holds installation tokens in memory only, minting each
// installation's once however many callers wait for it.
type tokenCache struct {
	c      *Client
	mu     sync.Mutex
	tokens map[int64]cachedToken
	group  singleflight.Group
}

func newTokenCache(c *Client) *tokenCache {
	return &tokenCache{c: c, tokens: map[int64]cachedToken{}}
}

func (t *tokenCache) cached(installation int64) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tok, ok := t.tokens[installation]
	if !ok || !t.c.opts.Now().Before(tok.expires.Add(-tokenRefreshMargin)) {
		return "", false
	}
	return tok.value, true
}

func (t *tokenCache) forget(installation int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.tokens, installation)
}

// get returns a valid token for installation, minting one if needed. The
// mint is shared, so it runs detached from any one caller's cancellation
// (bounded by the attempt timeout); a caller that gives up just stops
// waiting.
func (t *tokenCache) get(ctx context.Context, installation int64) (string, error) {
	if tok, ok := t.cached(installation); ok {
		return tok, nil
	}
	ch := t.group.DoChan(strconv.FormatInt(installation, 10), func() (any, error) {
		if tok, ok := t.cached(installation); ok {
			return tok, nil
		}
		mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), t.c.opts.Timeout)
		defer cancel()
		tok, err := t.c.mint(mctx, installation)
		if err != nil {
			return "", err
		}
		t.mu.Lock()
		t.tokens[installation] = tok
		t.mu.Unlock()
		return tok.value, nil
	})
	select {
	case res := <-ch:
		if res.Err != nil {
			return "", res.Err
		}
		return res.Val.(string), nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// mint exchanges the App JWT for an installation access token. It is not
// retried here: the call it serves is.
func (c *Client) mint(ctx context.Context, installation int64) (cachedToken, error) {
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	r := request{
		op:           "tokens.create",
		installation: installation,
		appAuth:      true,
		method:       http.MethodPost,
		path:         "/app/installations/" + strconv.FormatInt(installation, 10) + "/access_tokens",
		out:          &out,
		// Minting twice is harmless: the unused token just expires.
		idempotent: true,
	}
	auth, err := c.authorization(ctx, r)
	if err != nil {
		return cachedToken{}, err
	}
	if err := c.send(ctx, r, auth); err != nil {
		return cachedToken{}, err
	}
	if out.Token == "" || out.ExpiresAt.IsZero() {
		return cachedToken{}, &APIError{Op: r.op, Kind: app.ErrGitHubUnavailable, Message: "no token in the answer"}
	}
	c.log.InfoContext(ctx, "github installation token minted", "installation_id", installation, "expires_at", out.ExpiresAt)
	return cachedToken{value: out.Token, expires: out.ExpiresAt}, nil
}
