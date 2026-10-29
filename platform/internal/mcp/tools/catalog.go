package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	identity "go.klarlabs.de/glossa/platform/internal/identity/domain"
	"go.klarlabs.de/glossa/platform/internal/mcp/app"
	"go.klarlabs.de/glossa/platform/internal/mcp/domain"
)

// selectors are the arguments the audit ledger may record verbatim
// across every tool: ids, keys and enumerations. Free text — a TM
// query, a text to recognize terms in, a message's source, a proposed
// translation, a description — is never here, so the ledger records its
// length and nothing else (RFC 0005 §7.2, §11).
var selectors = []string{
	"project", "key", "key_prefix", "namespace", "state", "locale", "source_locale", "target_locale",
	"branch", "ref", "layer", "severity", "environment", "release", "message_key", "unused",
	"missing_in", "outdated_in", "waived", "limit",
	// The write tools' own selectors. `text`, `source`, `description`
	// and anything else that can carry message text stays out.
	"syntax", "base_revision", "source_revision", "max_length", "select",
	// The release tools'. An idempotency key is a client's own opaque
	// string and is exactly what makes a replayed publish legible in the
	// ledger; `note` is free text a person wrote and stays out.
	"idempotency_key",
}

// CatalogSearchName is the catalog search tool's wire name.
const CatalogSearchName = "catalog_search"

type catalogSearchArgs struct {
	Project    string `json:"project"`
	KeyPrefix  string `json:"key_prefix"`
	Namespace  string `json:"namespace"`
	State      string `json:"state"`
	MissingIn  string `json:"missing_in"`
	OutdatedIn string `json:"outdated_in"`
	Unused     bool   `json:"unused"`
	Limit      *int   `json:"limit"`
	Cursor     string `json:"cursor"`
}

// CatalogSearchResult is a page of a project's messages.
type CatalogSearchResult struct {
	Project  string           `json:"project"`
	Messages []MessageSummary `json:"messages"`
	// NextCursor continues the search; "" on the last page.
	NextCursor string `json:"next_cursor,omitempty"`
}

// catalogSearch lists a project's messages.
func catalogSearch(c Catalog) app.Tool {
	return app.Tool{
		Name:  CatalogSearchName,
		Title: "Search the catalog",
		Description: "List a project's messages by key prefix, namespace, state, or by translation " +
			"coverage in a locale (missing_in, outdated_in), or the active messages no build uses " +
			"(unused). Results are in key order and paginated: pass the returned next_cursor to " +
			"continue. Free-text search over message content is not available; search by key prefix " +
			"or namespace, then read a message with message_get.",
		Toolset:     domain.ToolsetRead,
		Permission:  identity.PermCatalogRead,
		Selectors:   selectors,
		ReadOnly:    true,
		InputSchema: json.RawMessage(catalogSearchSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a catalogSearchArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			project, err := projectOf(a.Project)
			if err != nil {
				return app.Result{}, err
			}
			limit, err := limitOf(a.Limit, MaxLimit)
			if err != nil {
				return app.Result{}, err
			}
			if a.MissingIn != "" && a.OutdatedIn != "" {
				return app.Result{}, invalid("missing_in", "cannot be combined with outdated_in")
			}
			if a.Unused && (a.MissingIn != "" || a.OutdatedIn != "") {
				return app.Result{}, invalid("unused", "cannot be combined with missing_in or outdated_in")
			}
			f := CatalogFilter{
				KeyPrefix: a.KeyPrefix, Namespace: a.Namespace, State: a.State,
				MissingIn: a.MissingIn, OutdatedIn: a.OutdatedIn, Unused: a.Unused,
			}
			msgs, next, err := c.SearchMessages(ctx, project, f, a.Cursor, limit)
			if err != nil {
				return app.Result{}, err
			}
			res := CatalogSearchResult{Project: a.Project, Messages: msgs, NextCursor: next}
			if res.Messages == nil {
				res.Messages = []MessageSummary{}
			}
			more := ""
			if next != "" {
				more = "; more follow, pass next_cursor to continue"
			}
			return app.Result{
				Explanation: fmt.Sprintf("Found %s%s.", plural(len(res.Messages), "message", "messages"), more),
				Data:        res,
			}, nil
		},
	}
}

const catalogSearchSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id."},
    "key_prefix": {"type": "string", "maxLength": 200, "description": "Keys starting with this, e.g. \"checkout.\"."},
    "namespace": {"type": "string", "maxLength": 200},
    "state": {"type": "string", "enum": ["active", "proposed", "obsolete"]},
    "missing_in": {"type": "string", "maxLength": 35, "description": "Only messages with no translation in this locale."},
    "outdated_in": {"type": "string", "maxLength": 35, "description": "Only messages whose translation in this locale is behind the source."},
    "unused": {"type": "boolean", "description": "Only active messages no current build refers to. Not combinable with missing_in or outdated_in; only key_prefix narrows it."},
    "limit": {"type": "integer", "minimum": 1, "maximum": 100, "description": "Page size; 20 by default."},
    "cursor": {"type": "string", "description": "The next_cursor of the previous page."}
  },
  "required": ["project"],
  "additionalProperties": false
}`

// MessageGetName is the message read tool's wire name.
const MessageGetName = "message_get"

type messageGetArgs struct {
	Project string `json:"project"`
	Key     string `json:"key"`
	Branch  string `json:"branch"`
	Limit   *int   `json:"limit"`
}

// MessageGetResult is one message with where it is used.
type MessageGetResult struct {
	Project string `json:"project"`
	MessageDetail
	// Usages are the first few places the product asks for the message;
	// usages_get returns them all.
	Usages    []Usage `json:"usages"`
	UsageMore bool    `json:"more_usages"`
	// Captures counts the current captures that show the message
	// (RFC 0004 §2.2).
	Captures    int  `json:"captures"`
	CaptureMore bool `json:"more_captures"`
	// Neighbours are messages shown together with this one, which is
	// what tells an agent what the screen is about.
	Neighbours []Neighbour `json:"neighbours,omitempty"`
}

// messageNeighbours bounds the extras message_get attaches. They are
// context, not the answer: an agent that wants all of them asks
// usages_get.
const (
	messageUsages     = 5
	messageCaptures   = 50
	messageNeighbours = 10
)

// messageGet reads one message with its usages, capture count and
// neighbours (RFC 0005 §7.3).
func messageGet(c Catalog, u UsageReader) app.Tool {
	return app.Tool{
		Name:  MessageGetName,
		Title: "Read a message",
		Description: "Read one source message by key: its text, the arguments it declares, its " +
			"description and max_length, the first few places the product uses it, how many " +
			"captures show it, and the messages shown together with it. Use usages_get for every " +
			"usage and translation_get for a locale's text.",
		Toolset:     domain.ToolsetRead,
		Permission:  identity.PermCatalogRead,
		Selectors:   selectors,
		ReadOnly:    true,
		InputSchema: json.RawMessage(messageGetSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a messageGetArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			project, err := projectOf(a.Project)
			if err != nil {
				return app.Result{}, err
			}
			if err := required("key", a.Key); err != nil {
				return app.Result{}, err
			}
			usageLimit, err := limitOf(a.Limit, MaxLimit)
			if err != nil {
				return app.Result{}, err
			}
			if a.Limit == nil {
				usageLimit = messageUsages
			}
			m, err := c.Message(ctx, project, a.Key)
			if err != nil {
				return app.Result{}, err
			}
			res := MessageGetResult{Project: a.Project, MessageDetail: m, Usages: []Usage{}}
			res.attach(ctx, u, project, a.Key, a.Branch, usageLimit, m.ID)
			return app.Result{
				Explanation: fmt.Sprintf("%s is %s in %s, used in %s.",
					m.Key, m.State, namespaceOf(m.Namespace), plural(len(res.Usages), "place", "places")),
				Data: res,
			}, nil
		},
	}
}

// attach fills in what Context knows about the message. Context is a
// separate context and a separate upload: a project that never ran the
// collector has no usages, and that is an empty list rather than a
// failed read.
func (r *MessageGetResult) attach(
	ctx context.Context, u UsageReader, project uuid.UUID, key, branch string, limit int, id string,
) {
	if u == nil {
		return
	}
	if got, err := u.OfKey(ctx, project, key, branch, limit); err == nil {
		r.Usages, r.UsageMore = got.Usages, got.Truncated
	}
	if n, more, err := u.CaptureCount(ctx, project, key, messageCaptures); err == nil {
		r.Captures, r.CaptureMore = n, more
	}
	if mid, err := uuid.Parse(id); err == nil {
		if ns, err := u.Neighbours(ctx, project, mid, messageNeighbours); err == nil {
			r.Neighbours = ns
		}
	}
}

func namespaceOf(ns string) string {
	if ns == "" {
		return "the default namespace"
	}
	return "namespace " + ns
}

const messageGetSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id."},
    "key": {"type": "string", "maxLength": 200, "description": "The message key."},
    "branch": {"type": "string", "maxLength": 255, "description": "Read usages from this branch's view; the default branch when absent."},
    "limit": {"type": "integer", "minimum": 1, "maximum": 100, "description": "How many usages to attach; 5 by default."}
  },
  "required": ["project", "key"],
  "additionalProperties": false
}`

// MessageUpsertName is the source write tool's wire name.
const MessageUpsertName = "message_upsert"

type messageUpsertArgs struct {
	Project      string  `json:"project"`
	Key          string  `json:"key"`
	Text         string  `json:"text"`
	Syntax       string  `json:"syntax"`
	Namespace    *string `json:"namespace"`
	Description  *string `json:"description"`
	MaxLength    *int    `json:"max_length"`
	BaseRevision *int    `json:"base_revision"`
}

// messageUpsert creates or revises a source message (RFC 0005 §7.3).
//
// It is Catalog's own upsert — the one `glossa push` and the REST API
// call — so a message an agent writes is indistinguishable from one CI
// pushed, down to the source revision it makes and the events it
// publishes. What the tool leaves out is as deliberate as what it has:
// no rename, no obsolete and no delete, because destroying data is not
// a capability M4 gives an agent (RFC 0005 §7.2).
func messageUpsert(c CatalogWriter) app.Tool {
	return app.Tool{
		Name:  MessageUpsertName,
		Title: "Create or revise a message",
		Description: "Create a source message, or revise the text of one that exists. The key " +
			"identifies it, and sending the same text twice changes nothing. Revising the text " +
			"makes a new source revision, which marks every translation of the message outdated: " +
			"read the message with message_get first and pass its revision as base_revision to be " +
			"told, rather than to overwrite, when someone changed it meanwhile. Needs a write " +
			"session on a token carrying the write scope.",
		Toolset:     domain.ToolsetWrite,
		Permission:  identity.PermCatalogWrite,
		Selectors:   selectors,
		InputSchema: json.RawMessage(messageUpsertSchema),
		Handler: func(ctx context.Context, _ app.Session, raw json.RawMessage) (app.Result, error) {
			var a messageUpsertArgs
			if err := decode(raw, &a); err != nil {
				return app.Result{}, err
			}
			project, err := projectOf(a.Project)
			if err != nil {
				return app.Result{}, err
			}
			if err := required("key", a.Key); err != nil {
				return app.Result{}, err
			}
			if err := boundedText("text", a.Text); err != nil {
				return app.Result{}, err
			}
			if a.BaseRevision != nil && *a.BaseRevision < 1 {
				return app.Result{}, invalid("base_revision", "is a source revision, which starts at 1")
			}
			if a.MaxLength != nil && *a.MaxLength < 1 {
				return app.Result{}, invalid("max_length", "must be at least 1")
			}
			m, err := c.UpsertMessage(ctx, project, MessageUpsert{
				Key: a.Key, Namespace: a.Namespace, Description: a.Description, MaxLength: a.MaxLength,
				Text: a.Text, Syntax: a.Syntax, BaseRevision: a.BaseRevision,
			})
			if err != nil {
				return app.Result{}, err
			}
			return app.Result{
				Explanation: fmt.Sprintf("%s was %s; it is now at source revision %d.",
					m.Key, upsertVerb(m.Status), m.Revision),
				Data:     m,
				Affected: []string{m.ID},
			}, nil
		},
	}
}

// upsertVerb reads an upsert status back as a sentence.
func upsertVerb(status string) string {
	switch status {
	case "created":
		return "created"
	case "revised":
		return "revised, making a new source revision"
	case "updated":
		return "updated without changing its text"
	default:
		return "already exactly this, so nothing changed"
	}
}

const messageUpsertSchema = `{
  "type": "object",
  "properties": {
    "project": {"type": "string", "format": "uuid", "description": "The project's id."},
    "key": {"type": "string", "maxLength": 200, "description": "The message key, e.g. \"checkout.pay\"."},
    "text": {"type": "string", "maxLength": 4000, "description": "The source text, in the project's syntax."},
    "syntax": {"type": "string", "enum": ["mf2", "icu"], "description": "The text's syntax; the project's default when absent."},
    "namespace": {"type": "string", "maxLength": 200, "description": "The namespace; left as it is when absent."},
    "description": {"type": "string", "maxLength": 2000, "description": "What the message is for, for translators; left as it is when absent."},
    "max_length": {"type": "integer", "minimum": 1, "description": "The longest a translation may be; left as it is when absent."},
    "base_revision": {"type": "integer", "minimum": 1, "description": "The source revision read before revising; the write is refused if the message moved on."}
  },
  "required": ["project", "key", "text"],
  "additionalProperties": false
}`
