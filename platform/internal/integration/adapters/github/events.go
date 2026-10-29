package github

import (
	"encoding/json"
	"fmt"
	"time"
)

// Event is a parsed webhook payload Glossa handles.
type Event interface {
	// Name is "event.action", e.g. "pull_request.opened".
	Name() string
	// InstallationID resolves the tenant.
	InstallationID() int64
}

// Account is a user or organization.
type Account struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
	Type  string `json:"type"`
}

// Repository is a repository; Glossa keys it by ID, never by name.
type Repository struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	FullName      string  `json:"full_name"`
	Private       bool    `json:"private"`
	Fork          bool    `json:"fork"`
	DefaultBranch string  `json:"default_branch"`
	Owner         Account `json:"owner"`
}

// Installation is the App's installation on an account.
type Installation struct {
	ID                  int64   `json:"id"`
	AppID               int64   `json:"app_id"`
	Account             Account `json:"account"`
	TargetType          string  `json:"target_type"`
	RepositorySelection string  `json:"repository_selection"`
}

// InstallationRef is the installation stub on repository events.
type InstallationRef struct {
	ID int64 `json:"id"`
}

// InstallationEvent is `installation` (created, deleted, suspend,
// unsuspend, new_permissions_accepted).
type InstallationEvent struct {
	Action       string       `json:"action"`
	Installation Installation `json:"installation"`
	// Repositories are those selected at creation.
	Repositories []Repository `json:"repositories"`
	Sender       Account      `json:"sender"`
}

// Name implements Event.
func (e *InstallationEvent) Name() string { return "installation." + e.Action }

// InstallationID implements Event.
func (e *InstallationEvent) InstallationID() int64 { return e.Installation.ID }

// InstallationRepositoriesEvent is `installation_repositories` (added,
// removed).
type InstallationRepositoriesEvent struct {
	Action              string       `json:"action"`
	Installation        Installation `json:"installation"`
	RepositorySelection string       `json:"repository_selection"`
	RepositoriesAdded   []Repository `json:"repositories_added"`
	RepositoriesRemoved []Repository `json:"repositories_removed"`
	Sender              Account      `json:"sender"`
}

// Name implements Event.
func (e *InstallationRepositoriesEvent) Name() string { return "installation_repositories." + e.Action }

// InstallationID implements Event.
func (e *InstallationRepositoriesEvent) InstallationID() int64 { return e.Installation.ID }

// GitRef is a pull request's head or base.
type GitRef struct {
	Ref   string `json:"ref"`
	SHA   string `json:"sha"`
	Label string `json:"label"`
	// Repo is nil when a fork was deleted.
	Repo *Repository `json:"repo"`
}

// PullRequest is the pull request on a `pull_request` event.
type PullRequest struct {
	ID             int64   `json:"id"`
	Number         int     `json:"number"`
	State          string  `json:"state"`
	Title          string  `json:"title"`
	Draft          bool    `json:"draft"`
	Merged         bool    `json:"merged"`
	MergeCommitSHA string  `json:"merge_commit_sha"`
	HTMLURL        string  `json:"html_url"`
	User           Account `json:"user"`
	Head           GitRef  `json:"head"`
	Base           GitRef  `json:"base"`
	// CreatedAt is when the pull request was opened. It is the one fact
	// a policy's grace is measured against (RFC 0005 §4.3), and it has
	// to come from the payload: a pull request opened before the App was
	// installed, or before the policy was saved, is exactly the case the
	// grace exists for, and any clock of ours would call it new.
	CreatedAt time.Time `json:"created_at"`
}

// PullRequestEvent is `pull_request` (opened, synchronize, reopened,
// closed).
type PullRequestEvent struct {
	Action       string          `json:"action"`
	Number       int             `json:"number"`
	PullRequest  PullRequest     `json:"pull_request"`
	Repository   Repository      `json:"repository"`
	Installation InstallationRef `json:"installation"`
	Sender       Account         `json:"sender"`
}

// Name implements Event.
func (e *PullRequestEvent) Name() string { return "pull_request." + e.Action }

// InstallationID implements Event.
func (e *PullRequestEvent) InstallationID() int64 { return e.Installation.ID }

// FromFork reports whether the head lives in another repository: such a
// pull request's CI gets no OIDC token, so its check is neutral.
func (e *PullRequestEvent) FromFork() bool {
	h := e.PullRequest.Head.Repo
	return h == nil || h.ID != e.Repository.ID
}

// CheckRunPullRequest is a pull request a check run belongs to.
type CheckRunPullRequest struct {
	Number int    `json:"number"`
	Head   GitRef `json:"head"`
	Base   GitRef `json:"base"`
}

// CheckRunPayload is the check run on a `check_run` event.
type CheckRunPayload struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	HeadSHA    string `json:"head_sha"`
	ExternalID string `json:"external_id"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	// App is the check run's owner: act only on the Glossa App's own.
	App struct {
		ID int64 `json:"id"`
	} `json:"app"`
	PullRequests []CheckRunPullRequest `json:"pull_requests"`
}

// CheckRunEvent is `check_run` (rerequested).
type CheckRunEvent struct {
	Action       string          `json:"action"`
	CheckRun     CheckRunPayload `json:"check_run"`
	Repository   Repository      `json:"repository"`
	Installation InstallationRef `json:"installation"`
	Sender       Account         `json:"sender"`
}

// Name implements Event.
func (e *CheckRunEvent) Name() string { return "check_run." + e.Action }

// InstallationID implements Event.
func (e *CheckRunEvent) InstallationID() int64 { return e.Installation.ID }

// handled lists the events and actions Glossa acts on.
var handled = map[string]map[string]bool{
	"installation":              {"created": true, "deleted": true, "suspend": true, "unsuspend": true, "new_permissions_accepted": true},
	"installation_repositories": {"added": true, "removed": true},
	"pull_request":              {"opened": true, "synchronize": true, "reopened": true, "closed": true},
	"check_run":                 {"rerequested": true},
}

// ParseEvent decodes a verified delivery into its typed payload. Events
// and actions Glossa does not handle answer ErrWebhookIgnored; payloads
// missing the IDs Glossa keys on answer ErrWebhookPayload.
func ParseEvent(d Delivery) (Event, error) {
	actions, ok := handled[d.Event]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrWebhookIgnored, d.Event)
	}
	var head struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(d.Body, &head); err != nil {
		return nil, fmt.Errorf("%w: %s is not JSON", ErrWebhookPayload, d.Event)
	}
	if !actions[head.Action] {
		return nil, fmt.Errorf("%w: %s.%s", ErrWebhookIgnored, d.Event, safeAction(head.Action))
	}
	var (
		ev    Event
		check func() bool
	)
	switch d.Event {
	case "installation":
		e := &InstallationEvent{}
		ev, check = e, func() bool { return e.Installation.ID > 0 && reposHaveIDs(e.Repositories) }
	case "installation_repositories":
		e := &InstallationRepositoriesEvent{}
		ev, check = e, func() bool {
			return e.Installation.ID > 0 && reposHaveIDs(e.RepositoriesAdded) && reposHaveIDs(e.RepositoriesRemoved)
		}
	case "pull_request":
		e := &PullRequestEvent{}
		ev, check = e, func() bool {
			return e.Installation.ID > 0 && e.Repository.ID > 0 && e.Number > 0 &&
				e.PullRequest.Number == e.Number && shaPattern.MatchString(e.PullRequest.Head.SHA)
		}
	case "check_run":
		e := &CheckRunEvent{}
		ev, check = e, func() bool {
			return e.Installation.ID > 0 && e.Repository.ID > 0 && e.CheckRun.ID > 0 && shaPattern.MatchString(e.CheckRun.HeadSHA)
		}
	}
	if err := json.Unmarshal(d.Body, ev); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrWebhookPayload, ev.Name())
	}
	if !check() {
		return nil, fmt.Errorf("%w: %s lacks an installation, repository or head", ErrWebhookPayload, ev.Name())
	}
	return ev, nil
}

func reposHaveIDs(repos []Repository) bool {
	for _, r := range repos {
		if r.ID <= 0 {
			return false
		}
	}
	return true
}

// safeAction keeps an unknown action out of logs unless it looks like
// one.
func safeAction(a string) string {
	if len(a) > 40 {
		return "?"
	}
	for _, r := range a {
		if (r < 'a' || r > 'z') && r != '_' {
			return "?"
		}
	}
	return a
}
