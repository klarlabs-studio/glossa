package v0

import (
	"fmt"
	"sort"
	"time"
)

// DBPlan is the import of a restored v0.3 database (RFC 0006 §7.2): the
// API mode's plan, with descriptions and provenance filled in, plus what
// other waves complete — the users as invitation plans (Identity sends
// them, wave 3) and the history as an audit-entry plan (Audit imports
// it, wave 4). Nothing here sends an invitation or writes an audit
// entry.
type DBPlan struct {
	Plan
	Restore Restore
	Tenant  Tenant
	Project Project
	// LocaleInfo is every v0.3 locale of the project, with its label.
	LocaleInfo   []LocaleInfo
	Invitations  []Invitation
	AuditEntries []AuditEntry
	NotCarried   []NotCarried
	Warnings     []string
}

// LocaleInfo is a v0.3 locale as the report shows it.
type LocaleInfo struct {
	Code    string `json:"code"`
	V0Code  string `json:"v0_code"`
	Label   string `json:"label"`
	Enabled bool   `json:"enabled"`
	Source  bool   `json:"source"`
}

// Invitation is a v0.3 user as an invitation Identity sends. Status is
// "planned", or "held" when sending it as v0.3 had it would change what
// the person may do; Reason says why. `glossa import --invite` sends
// the planned ones and reports each as "invited", "exists" (the address
// is already a member or invited: nothing is sent twice) or "failed".
type Invitation struct {
	V0UserID    string    `json:"v0_user_id"`
	Email       string    `json:"email"`
	Roles       []string  `json:"roles"`
	Locales     []string  `json:"locales"`
	V0Role      string    `json:"v0_role"`
	V0Locales   []string  `json:"v0_locales"`
	V0CreatedAt time.Time `json:"v0_created_at"`
	Status      string    `json:"status"`
	Reason      string    `json:"reason,omitempty"`
	// MemberID is the platform's member or invitation, once sent or
	// found.
	MemberID string `json:"member_id,omitempty"`
}

// The statuses of an invitation.
const (
	InvitationPlanned = "planned"
	InvitationHeld    = "held"
	InvitationInvited = "invited"
	InvitationExists  = "exists"
	InvitationFailed  = "failed"
)

// AuditActionTranslationChanged is the action of an imported v0.3
// history entry (RFC 0006 §7.2).
const AuditActionTranslationChanged = "v0.translation.changed"

// AuditEntry is one v0.3 audit_log row as an imported audit entry
// (wave 4). V0ID is the idempotency key: one entry per v0.3 row however
// often the plan is made. Key and Locale reference the imported
// translation; Unresolved says why there is none.
//
// Before and After are v0.3's text. RFC 0006 §6.1 keeps text out of
// audit entries, so the plan carries them for the Audit import to
// place (the revision-log-like store it points at), not to write into
// an entry's summary.
type AuditEntry struct {
	V0ID            int64     `json:"v0_id"`
	Action          string    `json:"action"`
	Actor           string    `json:"actor"`
	OccurredAt      time.Time `json:"occurred_at"`
	Key             string    `json:"key,omitempty"`
	Locale          string    `json:"locale,omitempty"`
	V0TranslationID string    `json:"v0_translation_id,omitempty"`
	Before          *string   `json:"before"`
	After           *string   `json:"after"`
	Unresolved      string    `json:"unresolved,omitempty"`
}

// NotCarried is a v0.3 field or table the import deliberately does not
// carry, with how many rows have it and why.
type NotCarried struct {
	What  string `json:"what"`
	Count int    `json:"count,omitempty"`
	Why   string `json:"why"`
}

// BuildDBPlan plans the import of s into a project whose source locale
// is source. only, when set, limits the target locales as --locales does.
func BuildDBPlan(source string, s Snapshot, only map[string]bool) (DBPlan, error) {
	out := DBPlan{Restore: s.Restore, Tenant: s.Tenant, Project: s.Project}
	bundles, canon, err := s.bundles(source, only)
	if err != nil {
		return DBPlan{}, err
	}
	if out.Plan, err = BuildPlan(source, bundles); err != nil {
		return DBPlan{}, err
	}
	out.describe(s.Keys)
	out.provenance(s.Translations, canon)
	out.locales(source, s)
	out.Invitations = invitations(s.Users)
	out.AuditEntries = auditEntries(s.History, source, only)
	out.NotCarried = notCarried(s, source)
	return out, nil
}

// bundles shapes the restore like the read API: per canonical locale,
// every key (empty when untranslated) with its value and status.
func (s Snapshot) bundles(source string, only map[string]bool) (map[string]Bundle, map[string]string, error) {
	canon := map[string]string{}
	bundles := map[string]Bundle{}
	for _, l := range s.Locales {
		c, err := Canonical(l.Code)
		if err != nil {
			return nil, nil, fmt.Errorf("v0.3 locale %q is not BCP 47: %w", l.Code, err)
		}
		canon[l.Code] = c
		if c != source && only != nil && !only[c] {
			continue
		}
		b := Bundle{Project: s.Project.Slug, Locale: l.Code, Messages: map[string]string{}, Statuses: map[string]string{}}
		for _, k := range s.Keys {
			b.Messages[k.Key] = ""
		}
		bundles[c] = b
	}
	for _, t := range s.Translations {
		if b, ok := bundles[canon[t.Locale]]; ok {
			b.Messages[t.Key], b.Statuses[t.Key] = t.Value, t.Status
		}
	}
	return bundles, canon, nil
}

func (p *DBPlan) describe(keys []Key) {
	desc := map[string]string{}
	for _, k := range keys {
		desc[k.Key] = k.Description
	}
	for i := range p.Messages {
		p.Messages[i].Description = desc[p.Messages[i].Key]
	}
}

func (p *DBPlan) provenance(rows []Row, canon map[string]string) {
	byKey := map[string]Row{}
	for _, r := range rows {
		byKey[canon[r.Locale]+"\x00"+r.Key] = r
	}
	for i := range p.Translations {
		tr := &p.Translations[i]
		r := byKey[tr.Locale+"\x00"+tr.Key]
		var by any
		switch {
		case r.UpdatedBy != "":
			by = "v0:" + r.UpdatedBy
		case r.LastActorKind == "ai" || r.LastActorKind == "system":
			by = Actor("", r.LastActorKind, r.LastActorLabel)
		}
		tr.Detail = map[string]any{
			"v0_status":         orNone(r.Status),
			"v0_updated_by":     by,
			"v0_updated_at":     r.UpdatedAt.UTC().Format(time.RFC3339),
			"v0_translation_id": r.ID,
		}
	}
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// Actor spells a v0.3 actor for provenance and audit entries:
// "v0:<user id>" for a person, "v0:ai:<provider>" and
// "v0:system:<label>" for v0.3's non-human writers, and "v0:unknown"
// when v0.3 recorded nobody (an API-key write, or a deleted user's
// row). An actor is never invented.
func Actor(userID, kind, label string) string {
	suffix := ""
	if label != "" {
		suffix = ":" + label
	}
	switch {
	case kind == "ai" || kind == "system":
		return "v0:" + kind + suffix
	case userID != "":
		return "v0:" + userID
	}
	return "v0:unknown"
}

func (p *DBPlan) locales(source string, s Snapshot) {
	for _, l := range s.Locales {
		c, _ := Canonical(l.Code)
		p.LocaleInfo = append(p.LocaleInfo, LocaleInfo{Code: c, V0Code: l.Code, Label: l.Label, Enabled: l.Enabled, Source: c == source})
		if !l.Enabled {
			p.Warnings = append(p.Warnings, fmt.Sprintf(
				"v0.3 locale %s (%s) was disabled; its translations are imported all the same, and the platform has no disabled locale, so remove %s from the project if it must not ship",
				c, l.Label, c))
		}
	}
	sort.Slice(p.LocaleInfo, func(i, j int) bool {
		a, b := p.LocaleInfo[i], p.LocaleInfo[j]
		if a.Source != b.Source {
			return a.Source
		}
		return a.Code < b.Code
	})
	if def, err := Canonical(s.Project.DefaultLocale); err == nil && def != source {
		p.Warnings = append(p.Warnings, fmt.Sprintf(
			"the v0.3 project's default locale is %s, the platform project's source locale is %s: %s's text becomes the source messages",
			def, source, source))
	}
}

// invitations maps v0.3 users (RFC 0006 §7.2): admin → admin, every
// locale (a v0.3 admin edits any locale, whatever users.locales says);
// translator → translator with its locales. A v0.3 translator with no
// locales edits nothing, and the platform reads an empty locale scope as
// every locale, so that invitation is held rather than widened.
func invitations(users []User) []Invitation {
	out := make([]Invitation, 0, len(users))
	for _, u := range users {
		inv := Invitation{V0UserID: u.ID, Email: u.Email, V0Role: u.Role, V0Locales: nonNil(u.Locales),
			V0CreatedAt: u.CreatedAt, Roles: []string{}, Locales: []string{}, Status: "planned"}
		switch u.Role {
		case "admin":
			inv.Roles = []string{"admin"}
		case "translator":
			inv.Roles = []string{"translator"}
			inv.Locales, inv.Reason = translatorLocales(u.Locales)
			if inv.Reason != "" {
				inv.Status = "held"
			}
		default:
			inv.Status, inv.Reason = "held", fmt.Sprintf("v0.3 role %q has no mapping (v0.3 has admin and translator)", u.Role)
		}
		out = append(out, inv)
	}
	return out
}

func translatorLocales(codes []string) ([]string, string) {
	if len(codes) == 0 {
		return []string{}, "in v0.3 this translator could edit no locale; the platform reads an empty locale scope as every locale, " +
			"so sending it as planned would widen access: choose its locales, or drop it"
	}
	seen := map[string]bool{}
	out := []string{}
	for _, c := range codes {
		canon, err := Canonical(c)
		if err != nil {
			return []string{}, fmt.Sprintf("v0.3 locale %q in its scope is not a locale; fix the scope before sending", c)
		}
		if !seen[canon] {
			seen[canon] = true
			out = append(out, canon)
		}
	}
	sort.Strings(out)
	return out, ""
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func auditEntries(history []Change, source string, only map[string]bool) []AuditEntry {
	out := make([]AuditEntry, 0, len(history))
	for _, c := range history {
		e := AuditEntry{V0ID: c.ID, Action: AuditActionTranslationChanged, Actor: Actor(c.ChangedBy, c.ActorKind, c.ActorLabel),
			OccurredAt: c.ChangedAt.UTC(), Key: c.Key, V0TranslationID: c.TranslationID, Before: c.Before, After: c.After}
		if c.Key != "" {
			e.Locale = c.Locale
			if canon, err := Canonical(c.Locale); err == nil {
				e.Locale = canon
			}
			if e.Locale != source && only != nil && !only[e.Locale] {
				continue
			}
		} else {
			e.Unresolved = "its v0.3 translation no longer exists (v0.3's audit_log has no foreign key), so no key or locale can be named"
			if c.TranslationID == "" {
				e.Unresolved = "the v0.3 row names no translation"
			}
		}
		out = append(out, e)
	}
	return out
}

// notCarried lists, with reasons, every v0.3 field this import does not
// carry. A column missing from both the import and this list would be a
// silent loss; TestEveryUncarriedV0FieldIsReportedWithAReason pins it.
// The full dump is archived as the record of last resort (RFC 0006
// §7.2, §15 q8), so "not carried" never means "gone".
func notCarried(s Snapshot, source string) []NotCarried {
	sourceRows := 0
	for _, t := range s.Translations {
		if c, err := Canonical(t.Locale); err == nil && c == source {
			sourceRows++
		}
	}
	return []NotCarried{
		{What: "users.password_hash", Count: len(s.Users),
			Why: "credentials never travel: an invited person sets their own sign-in (passkey, password + TOTP, magic link) when accepting"},
		{What: "project_api_keys", Count: s.Uncarried.APIKeys,
			Why: "consumers get new delivery keys when they switch runtimes (RFC 0006 §7.2); v0.3 keys are hashes and can't be reissued"},
		{What: "ai_translation_providers", Count: s.Uncarried.AIProviders,
			Why: "their API keys are encrypted with v0.3's GLOSSA_SECRETS_KEY and must not travel; configure providers in the platform's AI routing"},
		{What: "analytics_events", Count: s.Uncarried.AnalyticsEvents,
			Why: "v0.3's funnel telemetry is not carried (RFC 0006 §7.2)"},
		{What: "keys.first_seen_at", Count: len(s.Keys),
			Why: "the platform's message API takes no creation time; the message starts at the import, and the date stays in the archived dump"},
		{What: "translations (source locale): status, updated_by, updated_at", Count: sourceRows,
			Why: "source text becomes the message, whose API takes no provenance detail; changes to it are in the audit-entry plan"},
		{What: "locales.label", Count: len(s.Locales),
			Why: "the platform names locales from CLDR and has no label field (the API's Locale is code and direction); the labels are in this report only"},
		{What: "locales.created_at", Count: len(s.Locales),
			Why: "a platform locale's creation is the import"},
		{What: "tenants",
			Why: "the platform organization already exists (the import writes into the project glossa.yaml names); the v0.3 tenant is in the report's source"},
		{What: "projects.name",
			Why: "the platform project already exists with its own name; the v0.3 name is in the report's source"},
		{What: "projects.created_at",
			Why: "the platform project has its own creation time"},
	}
}

// ReportSource is the v0.3 side of the report.
func (p DBPlan) ReportSource(dsn string) map[string]string {
	return map[string]string{
		"db": DescribeDSN(dsn), "tenant": p.Tenant.Slug, "project": p.Project.Slug, "project_name": p.Project.Name,
		"default_locale": p.Project.DefaultLocale,
	}
}

// OriginDetail is what every imported translation's origin_detail starts
// with: where the import read from.
func (p DBPlan) OriginDetail() map[string]any {
	return map[string]any{"source": "glossa-v0.3", "project": p.Project.Slug, "tenant": p.Tenant.Slug,
		"restore": p.Restore.DumpName, "restore_sha256": p.Restore.DumpSHA256}
}

// HeldInvitations counts held invitations.
func (p DBPlan) HeldInvitations() int {
	n := 0
	for _, i := range p.Invitations {
		if i.Status == "held" {
			n++
		}
	}
	return n
}

// UnresolvedHistory counts history entries without a translation.
func (p DBPlan) UnresolvedHistory() int {
	n := 0
	for _, e := range p.AuditEntries {
		if e.Unresolved != "" {
			n++
		}
	}
	return n
}
