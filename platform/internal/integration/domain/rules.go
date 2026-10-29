package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
)

// ItemKind is what one result of an import is about.
type ItemKind string

// Item kinds.
const (
	ItemMessage     ItemKind = "message"
	ItemTranslation ItemKind = "translation"
	ItemTMUnit      ItemKind = "tm_unit"
	ItemConcept     ItemKind = "concept"
)

// ItemStatus is what an import did (or, dry, would do) with one item.
type ItemStatus string

// Item statuses.
const (
	ItemCreated   ItemStatus = "created"
	ItemUpdated   ItemStatus = "updated"
	ItemUnchanged ItemStatus = "unchanged"
	// ItemConflict: stored data differs and the mode keeps it.
	ItemConflict ItemStatus = "conflict"
	// ItemInvalid: the item can't be imported (Code says why).
	ItemInvalid ItemStatus = "invalid"
)

// Item is one result of an import, in file order (Seq).
type Item struct {
	Seq    int
	Kind   ItemKind
	Key    string
	Locale string
	Status ItemStatus
	Code   string
	Detail string
	// Line and Column locate the item — or the problem that failed the
	// file — in the file (1-based; 0 unknown).
	Line   int
	Column int
	// Ref names the item in the format's own terms (formats.Position).
	Ref string
}

// At places the item where pos says it is in the file.
func (it Item) At(pos formats.Position) Item {
	it.Line, it.Column, it.Ref = pos.Line, pos.Column, truncateRunes(pos.Ref, MaxRefRunes)
	return it
}

// MaxRefRunes bounds an item's reference.
const MaxRefRunes = 500

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

// Result codes of items. Codes from the contexts an import writes
// through (structural_qa_failed, invalid_message_key, …) pass through.
const (
	// CodeSourceDiffers: the stored message's source differs from the
	// file's; merge keeps it, and its translations from the file too
	// (they were made for other text).
	CodeSourceDiffers = "source_differs"
	// CodeApprovedConflict: an approved translation has other text;
	// merge keeps it.
	CodeApprovedConflict = "approved_translation_conflict"
	// CodeForbidden: the requester may not import this item (a locale
	// outside their scope, a message to create without
	// integration.manage).
	CodeForbidden = "forbidden"
	// CodeMessageNotFound: a translation for a message that doesn't
	// exist (and isn't created by this import).
	CodeMessageNotFound = "message_not_found"
)

// Counts tallies items by status.
type Counts struct {
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Conflict  int `json:"conflict"`
	Invalid   int `json:"invalid"`
}

func (c *Counts) add(s ItemStatus) {
	switch s {
	case ItemCreated:
		c.Created++
	case ItemUpdated:
		c.Updated++
	case ItemUnchanged:
		c.Unchanged++
	case ItemConflict:
		c.Conflict++
	case ItemInvalid:
		c.Invalid++
	}
}

// Total is the number of items counted.
func (c Counts) Total() int { return c.Created + c.Updated + c.Unchanged + c.Conflict + c.Invalid }

// Summary is an import's counts, overall and per kind of item; an
// export's Written is the number of items in its file.
type Summary struct {
	Counts
	ByKind  map[ItemKind]Counts `json:"by_kind,omitempty"`
	Written int                 `json:"written,omitempty"`
}

// Add counts one item.
func (s *Summary) Add(it Item) {
	s.Counts.add(it.Status)
	if s.ByKind == nil {
		s.ByKind = map[ItemKind]Counts{}
	}
	c := s.ByKind[it.Kind]
	c.add(it.Status)
	s.ByKind[it.Kind] = c
}

// Review states as Localization names them.
const (
	stateDraft       = "draft"
	stateNeedsReview = "needs_review"
	stateApproved    = "approved"
	stateRejected    = "rejected"
)

// RequestedState is the review state an import asks for a translation
// the file puts in s: the file's own state, capped by what the
// requester may decide and the project's policy. An approval (or a
// rejection) is kept only for someone who may review the locale, or —
// approvals — when the project doesn't require review; otherwise the
// translation waits for review.
func RequestedState(s formats.State, canReview, reviewRequired bool) string {
	switch s {
	case formats.StateApproved:
		if canReview || !reviewRequired {
			return stateApproved
		}
		return stateNeedsReview
	case formats.StateRejected:
		if canReview {
			return stateRejected
		}
		return stateNeedsReview
	case formats.StateDraft:
		return stateDraft
	}
	return stateNeedsReview
}

// TranslationStatus maps Localization's result for one imported
// translation — its write status, or its item error code — onto an
// item status.
func TranslationStatus(writeStatus, errCode string) ItemStatus {
	switch {
	case errCode == CodeApprovedConflict:
		return ItemConflict
	case errCode != "":
		return ItemInvalid
	case writeStatus == "created":
		return ItemCreated
	case writeStatus == "unchanged":
		return ItemUnchanged
	}
	return ItemUpdated // revised, reviewed
}

// MessageAction is what an import does with a message of the file.
type MessageAction string

// Message actions.
const (
	ActionNone   MessageAction = ""
	ActionCreate MessageAction = "create"
	ActionRevise MessageAction = "revise"
)

// MessagePlan is the decision for one message of a file that carries
// source text.
type MessagePlan struct {
	Action MessageAction
	Status ItemStatus
	Code   string
	Detail string
}

// PlanMessage decides what happens to a file's message: a missing one
// is created (with integration.manage); one with the same source is
// unchanged; one whose source differs is revised in overwrite mode and a
// conflict otherwise — a translator's XLIFF never rewrites the source it
// was exported with. exists and sameSource describe the stored message.
func PlanMessage(exists, sameSource bool, mode Mode, canManage bool) MessagePlan {
	switch {
	case !exists && !canManage:
		return MessagePlan{Status: ItemInvalid, Code: CodeForbidden,
			Detail: "the message doesn't exist; creating messages needs integration.manage"}
	case !exists:
		return MessagePlan{Action: ActionCreate, Status: ItemCreated}
	case sameSource:
		return MessagePlan{Status: ItemUnchanged}
	case mode == ModeOverwrite && canManage:
		return MessagePlan{Action: ActionRevise, Status: ItemUpdated}
	}
	return MessagePlan{Status: ItemConflict, Code: CodeSourceDiffers,
		Detail: "the message's source differs from the file's; merge keeps it (overwrite revises it)"}
}

// Gettext keys. PO files key messages by their source text (msgid,
// disambiguated by msgctxt), and Glossa's keys are stable identifiers
// ([a-z0-9_-] segments joined by dots). POMessageKey derives one:
//
//	[<msgctxt slug>.]<msgid slug>_<hash>
//
// A slug is the text folded to ASCII lowercase (accents dropped, other
// scripts left out), runs of anything else turned into "_", trimmed and
// cut at a word boundary to 40 characters. The hash is the first 8 hex
// digits of SHA-256 over msgctxt, U+0004 and msgid — gettext's own
// separator — so equal texts in different contexts, and texts whose
// slugs collide, get different keys, and the same entry always gets the
// same key. "Add to cart" becomes add_to_cart_<hash>; a msgid without
// Latin letters or digits is just its hash.
func POMessageKey(msgctxt, msgid string) string {
	sum := sha256.Sum256([]byte(msgctxt + "\x04" + msgid))
	hash := hex.EncodeToString(sum[:])[:8]
	key := hash
	if s := slug(msgid); s != "" {
		key = s + "_" + hash
	}
	if c := slug(msgctxt); c != "" {
		key = c + "." + key
	}
	return key
}

const maxSlug = 40

// latinFold spells Latin letters that don't decompose into a base
// letter and marks.
var latinFold = strings.NewReplacer("ß", "ss", "ẞ", "SS", "æ", "ae", "Æ", "AE", "œ", "oe", "Œ", "OE",
	"ø", "o", "Ø", "O", "đ", "d", "Đ", "D", "ł", "l", "Ł", "L", "þ", "th", "Þ", "TH", "ı", "i")

func slug(s string) string {
	var b strings.Builder
	sep := false
	for _, r := range norm.NFKD.String(latinFold.Replace(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			if sep && b.Len() > 0 {
				b.WriteByte('_')
			}
			sep = false
			b.WriteRune(unicode.ToLower(r))
		default:
			sep = true
		}
	}
	out := b.String()
	if len(out) > maxSlug {
		out = out[:maxSlug]
		if i := strings.LastIndexByte(out, '_'); i > maxSlug/2 {
			out = out[:i]
		}
		out = strings.TrimRight(out, "_")
	}
	return out
}

// Fingerprint identifies an import's result: the file's digest and
// everything that shapes what it does (project, format, mode, options).
// Uploading the same file with the same options again reuses the first
// job's result instead of applying it twice.
func Fingerprint(fileSHA256 string, project string, f Format, m Mode, o Options) string {
	b, _ := json.Marshal(struct {
		File    string  `json:"file"`
		Project string  `json:"project"`
		Format  Format  `json:"format"`
		Mode    Mode    `json:"mode"`
		Options Options `json:"options"`
	}{fileSHA256, project, f, m, o})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
