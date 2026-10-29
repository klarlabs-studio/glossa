// Package nav shows what the Go extractor must not report.
package nav

import (
	"context"
	"fmt"
	"html/template"

	glossa "go.klarlabs.de/glossa/runtimes/go"
)

// homeKey is a constant, but only literals at the call site are usages.
const homeKey = "nav.home"

// l.T("fake.doc-comment", nil) in a doc comment is not a call.
func Links(ctx context.Context, c *glossa.Client, l *glossa.Localizer, id, section string) []string {
	// l.T("fake.line-comment", nil)
	/* c.T(ctx, "fake.block-comment", nil) */
	tmpl := template.Must(template.New("x").Parse(`{{t "fake.template-in-go-string"}}`))
	_ = tmpl
	return []string{
		l.T(id, nil),
		l.T(homeKey, nil),
		l.T("nav."+section, nil),
		l.T(fmt.Sprintf("nav.%s", section), nil),
		c.T(ctx, id, nil),
		"l.T(\"fake.string\", nil)",
		l.T("Not A Key", nil),
		l.Explain("fake.explain").ID,
		fmt.Sprint(T("fake.bare-function")),
		l.T("nav.control", nil),
	}
}

// T is a package function, not the runtime's method.
func T(id string) string { return id }
