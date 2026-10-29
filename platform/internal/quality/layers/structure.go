package layers

import (
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/quality/domain"
)

// Structure checks that every source message and translation parses
// into a valid MessageFormat 2 model (intent §29.1). The parsing itself
// happened when the project was read; this layer reports what did not
// survive it.
type Structure struct{}

// Layer implements Checker.
func (Structure) Layer() domain.Layer { return domain.LayerStructure }

// Check implements Checker.
func (Structure) Check(p *Project, _ checkpolicy.Policy) []domain.Finding {
	var out []domain.Finding
	for _, m := range p.Messages {
		if m.Invalid == nil {
			continue
		}
		out = append(out, domain.New(domain.Finding{
			Layer: domain.LayerStructure, Code: checkpolicy.CodeInvalidMessage, Severity: domain.Error,
			Locus: domain.Locus{
				Message: m.ID, Key: m.Key, Locale: p.SourceLocale, Namespace: m.Namespace, File: m.File,
			},
			Detail:  m.Invalid.Code,
			Message: "invalid message: " + m.Invalid.Detail,
		}))
	}
	for _, l := range p.TargetLocales() {
		for _, t := range p.SortedTranslations(l.Code) {
			if t.Invalid == nil {
				continue
			}
			out = append(out, domain.New(domain.Finding{
				Layer: domain.LayerStructure, Code: checkpolicy.CodeInvalidTranslation, Severity: domain.Error,
				Locus: domain.Locus{
					Message: messageID(p, t.Key), Key: t.Key, Locale: l.Code, Revision: t.Revision, File: t.File,
				},
				Detail:         t.Invalid.Code,
				Message:        "invalid translation: " + t.Invalid.Detail,
				SourceRevision: sourceRevision(t),
			}))
		}
	}
	return out
}

// messageID is the catalog ID of the message a translation belongs to,
// where the caller knows it.
func messageID(p *Project, key string) string {
	if m, ok := p.Message(key); ok {
		return m.ID
	}
	return ""
}
