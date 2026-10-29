package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// FingerprintPrefix marks a fingerprint in logs, waivers and the wire.
const FingerprintPrefix = "f_"

// Fingerprint identifies a finding: the same problem keeps the same
// fingerprint across re-runs, across `glossa check`, the PR check and
// the server job, and across edits that have nothing to do with it.
//
// It hashes exactly five things, and nothing else:
//
//   - the layer,
//   - the code,
//   - the message the finding is about — its catalog ID where the
//     caller has one, and its key where it does not (an offline
//     `glossa check` never has an ID),
//   - the locale, and
//   - the normalized subject: the argument, markup element or term the
//     finding names, NFC-normalized, trimmed and with internal
//     whitespace collapsed.
//
// What it deliberately leaves out is the point (RFC 0005 §2.1):
//
//   - the file, the line and the column, so reformatting a file or
//     moving a component does not re-open every waiver,
//   - the source revision, which invalidates waivers on its own terms
//     (§2.3) and must not silently mint new findings,
//   - the human message, the qualifier, the evidence and the fix, so
//     rewording an explanation — or rewording an unrelated message in
//     the same project — churns nothing,
//   - the severity, so tightening a policy does not orphan a waiver.
//
// Two distinct problems in one message stay distinct because the
// subject distinguishes them: two missing arguments are two findings.
//
// It generalizes domain.AnnotationFingerprint, which the PR check
// computes over (path, line, message) for a different job — not sending
// GitHub an annotation it already has — and which RFC 0005 §13 wave 4
// replaces when the PR check moves onto the layered report.
func Fingerprint(layer Layer, code string, l Locus, subject string) string {
	h := sha256.New()
	for _, part := range []string{
		Schema, // a domain separator: these bytes are a finding's identity
		string(layer),
		code,
		messageIdentity(l),
		l.Locale,
		NormalizeSubject(subject),
	} {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return FingerprintPrefix + hex.EncodeToString(h.Sum(nil)[:8])
}

// messageIdentity is the message a finding is about: its catalog ID
// where the caller knows it, its key otherwise.
//
// Every caller that can know the ID puts it here, which is what makes
// one finding one identity: the server always has it, and a `glossa
// check` or `glossa capture --check` against the server carries it in
// the snapshot it read (cli/qa.Project, layers.Project.Identify). A
// rename therefore never re-opens their waivers.
//
// Only a caller with no catalog to ask falls back to the key: `glossa
// check --offline` over the local catalog files, and a key the catalog
// has never seen. There a rename is a new finding, which is the honest
// answer — nothing said what that key is called — and such a print is
// not the server's for the same finding. Nobody holds a waiver offline,
// so nothing is lost by it.
func messageIdentity(l Locus) string {
	if l.Message != "" {
		return l.Message
	}
	return l.Key
}

// NormalizeSubject is the subject as the fingerprint sees it: NFC, no
// leading or trailing space, and every run of whitespace collapsed to
// one space. Case is kept, because a markup element and a term are both
// case-bearing and "Login" is not "login" to a termbase.
func NormalizeSubject(s string) string {
	return strings.Join(strings.Fields(norm.NFC.String(s)), " ")
}
