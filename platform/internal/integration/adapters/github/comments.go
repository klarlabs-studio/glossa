package github

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"go.klarlabs.de/glossa/platform/internal/integration/app"
)

// StickyMarker is the hidden line that identifies Glossa's one comment on
// a pull request.
const StickyMarker = "<!-- glossa:sticky -->"

const (
	commentsPerPage = 100
	// maxCommentPages bounds the marker search (10,000 comments).
	maxCommentPages = 100
	// maxCommentBody is GitHub's limit in characters.
	maxCommentBody = 65536
)

type commentJSON struct {
	ID                    int64  `json:"id"`
	Body                  string `json:"body"`
	PerformedViaGitHubApp *struct {
		ID int64 `json:"id"`
	} `json:"performed_via_github_app"`
}

// UpsertStickyComment implements app.GitHub.
func (c *Client) UpsertStickyComment(ctx context.Context, t app.GitHubTarget, pullRequest int, storedID int64, body string) (int64, error) {
	if pullRequest <= 0 || t.RepositoryID <= 0 {
		return 0, invalid("comments.upsert", "a sticky comment needs a repository and a pull request number")
	}
	body = withMarker(body)
	if storedID != 0 {
		err := c.updateComment(ctx, t, storedID, body)
		if !errors.Is(err, app.ErrGitHubNotFound) {
			return storedID, err
		}
		c.log.InfoContext(ctx, "github sticky comment gone, searching by marker", "installation_id", t.InstallationID,
			"repository_id", t.RepositoryID, "comment_id", storedID)
	}
	found, err := c.findSticky(ctx, t, pullRequest)
	if err != nil {
		return 0, err
	}
	if found != 0 {
		err := c.updateComment(ctx, t, found, body)
		if !errors.Is(err, app.ErrGitHubNotFound) {
			return found, err
		}
	}
	var out commentJSON
	err = c.do(ctx, request{
		op: "comments.create", tenant: t.TenantID, installation: t.InstallationID,
		method: http.MethodPost, path: repoPath(t.RepositoryID) + "/issues/" + strconv.Itoa(pullRequest) + "/comments",
		body: map[string]string{"body": body}, out: &out,
	})
	if err != nil {
		return 0, err
	}
	c.log.InfoContext(ctx, "github sticky comment created", "installation_id", t.InstallationID,
		"repository_id", t.RepositoryID, "comment_id", out.ID)
	return out.ID, nil
}

func (c *Client) updateComment(ctx context.Context, t app.GitHubTarget, id int64, body string) error {
	return c.do(ctx, request{
		op: "comments.update", tenant: t.TenantID, installation: t.InstallationID,
		method: http.MethodPatch, path: repoPath(t.RepositoryID) + "/issues/comments/" + strconv.FormatInt(id, 10),
		body: map[string]string{"body": body}, idempotent: true,
	})
}

// findSticky returns the App's comment carrying the marker (the oldest,
// should there ever be several), or 0. Comments by people that quote the
// marker are not ours and are skipped.
func (c *Client) findSticky(ctx context.Context, t app.GitHubTarget, pullRequest int) (int64, error) {
	for page := 1; page <= maxCommentPages; page++ {
		var out []commentJSON
		err := c.do(ctx, request{
			op: "comments.list", tenant: t.TenantID, installation: t.InstallationID,
			method: http.MethodGet, path: repoPath(t.RepositoryID) + "/issues/" + strconv.Itoa(pullRequest) + "/comments",
			query: url.Values{"per_page": {strconv.Itoa(commentsPerPage)}, "page": {strconv.Itoa(page)}},
			out:   &out, idempotent: true,
		})
		if err != nil {
			return 0, err
		}
		for _, cm := range out {
			if cm.PerformedViaGitHubApp != nil && cm.PerformedViaGitHubApp.ID == c.cfg.AppID && strings.Contains(cm.Body, StickyMarker) {
				return cm.ID, nil
			}
		}
		if len(out) < commentsPerPage {
			return 0, nil
		}
	}
	return 0, nil
}

// withMarker appends the marker unless body carries it, keeping the
// whole within GitHub's limit.
func withMarker(body string) string {
	if strings.Contains(body, StickyMarker) {
		return truncate(body, maxCommentBody)
	}
	suffix := "\n\n" + StickyMarker
	return truncate(body, maxCommentBody-len([]rune(suffix))) + suffix
}
