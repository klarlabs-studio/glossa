// Written by `go generate ./internal/systemtest/m3/...` — change the generator, not this file.
//
// Package receipt renders Brotwerk's receipt. It reuses the invoice
// page's copy, so `glossa extract` reports a second, server-side usage
// for those messages (RFC 0004 §2.1).
package receipt

import (
	glossa "go.klarlabs.de/glossa/runtimes/go"
)

// Render writes the receipt's lines for one reader.
func Render(l *glossa.Localizer) []string {
	return []string{
		l.T("invoice.croissant.placeholder", nil),
		l.T("invoice.biscuit.label", nil),
		l.T("invoice.address.title", nil),
		l.T("invoice.branch.help", nil),
		l.T("invoice.sourdough.activity", nil),
		l.T("invoice.account.title", nil),
		l.T("invoice.basket.title", nil),
	}
}
