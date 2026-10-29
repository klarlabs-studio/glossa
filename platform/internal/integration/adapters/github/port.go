package github

import (
	"errors"
	"io"
	"net/http"

	"go.klarlabs.de/glossa/platform/internal/integration/app"
)

// Webhooks adapts the verifier and the typed events to Integration's
// ports, so the application layer sees Glossa's own shapes and errors
// and never GitHub's. It is the anti-corruption layer for the webhook
// side, as the Client is for the API side.
type Webhooks struct{ v *WebhookVerifier }

// NewWebhooks returns the adapter for secret.
func NewWebhooks(secret []byte) (*Webhooks, error) {
	v, err := NewWebhookVerifier(secret)
	if err != nil {
		return nil, err
	}
	return &Webhooks{v: v}, nil
}

var (
	_ app.WebhookVerifier = (*Webhooks)(nil)
	_ app.WebhookEvents   = (*Webhooks)(nil)
)

// Verify implements app.WebhookVerifier: HMAC-SHA256 over the raw body,
// compared in constant time, before anything is parsed.
func (w *Webhooks) Verify(h http.Header, body io.Reader) (app.WebhookDelivery, error) {
	d, err := w.v.Verify(h, body)
	if err != nil {
		return app.WebhookDelivery{}, webhookError(err)
	}
	return app.WebhookDelivery{ID: d.ID, Event: d.Event, Body: d.Body}, nil
}

// Parse implements app.WebhookEvents.
func (w *Webhooks) Parse(d app.WebhookDelivery) (app.WebhookEvent, error) {
	ev, err := ParseEvent(Delivery{ID: d.ID, Event: d.Event, Body: d.Body})
	if err != nil {
		return app.WebhookEvent{}, webhookError(err)
	}
	out := app.WebhookEvent{Event: d.Event, InstallationID: ev.InstallationID()}
	switch e := ev.(type) {
	case *InstallationEvent:
		out.Action = e.Action
		out.AccountID, out.AccountLogin, out.AccountType = e.Installation.Account.ID, e.Installation.Account.Login, e.Installation.Account.Type
	case *InstallationRepositoriesEvent:
		out.Action = e.Action
		out.AccountID, out.AccountLogin, out.AccountType = e.Installation.Account.ID, e.Installation.Account.Login, e.Installation.Account.Type
		out.RepositoriesAdded = repositoryIDs(e.RepositoriesAdded)
		out.RepositoriesRemoved = repositoryIDs(e.RepositoriesRemoved)
	case *PullRequestEvent:
		out.Action, out.RepositoryID = e.Action, e.Repository.ID
		out.PullRequest, out.HeadRef, out.HeadSHA = e.Number, e.PullRequest.Head.Ref, e.PullRequest.Head.SHA
		out.Merged, out.FromFork = e.PullRequest.Merged, e.FromFork()
		out.PullRequestOpenedAt = e.PullRequest.CreatedAt.UTC()
	case *CheckRunEvent:
		out.Action, out.RepositoryID = e.Action, e.Repository.ID
		out.HeadSHA, out.CheckRunID, out.CheckRunName = e.CheckRun.HeadSHA, e.CheckRun.ID, e.CheckRun.Name
		if len(e.CheckRun.PullRequests) > 0 {
			out.PullRequest = e.CheckRun.PullRequests[0].Number
			out.HeadRef = e.CheckRun.PullRequests[0].Head.Ref
		}
	}
	return out, nil
}

func repositoryIDs(repos []Repository) []int64 {
	if len(repos) == 0 {
		return nil
	}
	out := make([]int64, 0, len(repos))
	for _, r := range repos {
		out = append(out, r.ID)
	}
	return out
}

// webhookError maps the adapter's sentinels onto the application's, so
// only Glossa's own errors cross the boundary.
func webhookError(err error) error {
	for from, to := range map[error]error{
		ErrWebhookSignature: app.ErrWebhookSignature,
		ErrWebhookTooLarge:  app.ErrWebhookTooLarge,
		ErrWebhookHeaders:   app.ErrWebhookHeaders,
		ErrWebhookIgnored:   app.ErrWebhookIgnored,
		ErrWebhookPayload:   app.ErrWebhookPayload,
	} {
		if errors.Is(err, from) {
			return to
		}
	}
	return err
}
