// Package tbx reads and writes termbases in TBX-Basic (ISO 30042:2019,
// TBX v3, DCA style) as a formats.Termbase. Reading also accepts the
// TBX-Basic DCT style and TBX 2008 ("martif": termEntry/langSet/tig),
// which most tools still export.
//
// Mapping:
//
//   - <tbx xml:lang>: the termbase's document language.
//   - <conceptEntry id>: a concept. IDs that are not XML NCNames (UUIDs
//     start with a digit) are written as "c-<id>" and read back without
//     the prefix.
//   - <descrip type="subjectField"> on the concept: its domain.
//   - <descrip type="definition">: a definition. In the <langSec> of its
//     locale when the concept has terms in that locale, else on the
//     concept with xml:lang (TBX needs at least one term per langSec);
//     without a locale, on the concept.
//   - <note> on the concept: its notes (notes on a langSec join them).
//   - <langSec xml:lang><termSec>: one term per termSec: <term>,
//     <termNote type="partOfSpeech">, <termNote
//     type="administrativeStatus">, <descrip type="context"> and <note>s
//     (usage notes).
//
// Term status ↔ administrativeStatus (TBX-Basic's four values):
//
//	preferred  ↔ preferredTerm-admn-sts
//	admitted   ↔ admittedTerm-admn-sts
//	deprecated ↔ deprecatedTerm-admn-sts
//	forbidden  ↔ supersededTerm-admn-sts
//
// TBX-Basic has no "forbidden": supersededTerm ("replaced, must no
// longer be used") is the closest value and the one TBX-Basic tools
// use for do-not-use terms, while deprecatedTerm stays "discouraged".
// Reading also accepts the TBX 2008 normativeAuthorization values and
// standardizedTerm/legalTerm/regulatedTerm (as preferred); a term
// without a status is admitted.
//
// partOfSpeech is written only with a TBX-Basic value (noun, verb,
// adjective, adverb, other); other values are written as "other". Case
// sensitivity has no TBX-Basic data category and is not carried.
package tbx

import (
	"strings"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
)

const (
	// Namespace is the TBX v3 (ISO 30042:2019) namespace.
	Namespace = "urn:iso:std:iso:30042:ed-2"
	format    = "tbx"
	idPrefix  = "c-"
)

var statusValues = map[formats.TermStatus]string{
	formats.TermPreferred:  "preferredTerm-admn-sts",
	formats.TermAdmitted:   "admittedTerm-admn-sts",
	formats.TermDeprecated: "deprecatedTerm-admn-sts",
	formats.TermForbidden:  "supersededTerm-admn-sts",
}

// parseStatus reads administrativeStatus or normativeAuthorization.
func parseStatus(v string) (formats.TermStatus, bool) {
	switch strings.TrimSuffix(strings.TrimSpace(v), "-admn-sts") {
	case "preferredTerm", "standardizedTerm", "legalTerm", "regulatedTerm":
		return formats.TermPreferred, true
	case "admittedTerm":
		return formats.TermAdmitted, true
	case "deprecatedTerm":
		return formats.TermDeprecated, true
	case "supersededTerm":
		return formats.TermForbidden, true
	}
	return "", false
}

var partsOfSpeech = map[string]bool{"noun": true, "verb": true, "adjective": true, "adverb": true, "other": true}
