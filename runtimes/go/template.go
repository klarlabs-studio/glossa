package glossa

import (
	"fmt"
	"html/template"
)

// Template functions for html/template and text/template, for email, PDF
// and CLI output. The maps have the untyped map[string]any type, so they
// are assignable to both packages' FuncMap.
//
//	{{t "email.welcome.subject" "name" .Name}}   message with name/value pairs
//	{{t "email.welcome.body" .Args}}             message with an Args map
//	{{td "email.footer" "Thanks!" "name" .Name}} message with an inline default
//	{{th "email.welcome.intro" "name" .Name}}    message as safe HTML (Localizer.HTML)
//	<html lang="{{lang}}" dir="{{dir}}">         active locale and direction
//
// Numbers, money and dates outside messages, for table cells, with MF2
// function options as name/value pairs (Localizer.Number and the others):
//
//	{{num .Quantity}}  {{num .Rate "maximumFractionDigits" 2}}
//	{{percent .Share}}
//	{{money .Total}}                              a glossa.Money
//	{{money .Net "EUR"}}                          a number or Decimal, and a currency
//	{{money .Net "JPY" "currencyDisplay" "code"}}
//	{{unit .Distance "kilometer"}}
//	{{date .IssuedAt "length" "long"}}  {{time .IssuedAt}}  {{datetime .IssuedAt}}
//
// Parse templates once with TemplateFuncs, then bind a Localizer per
// execution on a clone: html/template can only clone templates that were
// never executed, so keep the parsed template as an unexecuted base.
//
//	base := template.Must(template.New("").Funcs(glossa.TemplateFuncs()).ParseFS(emails, "*.html"))
//	tmpl := template.Must(base.Clone())
//	err := tmpl.Funcs(client.For(user.Locale).FuncMap()).ExecuteTemplate(w, "welcome.html", data)

// FuncMap returns the template functions bound to l.
func (l *Localizer) FuncMap() map[string]any {
	return map[string]any{
		"t": func(id string, args ...any) string {
			return l.T(id, l.templateArgs(id, args))
		},
		"td": func(id, defaultText string, args ...any) string {
			return l.T(id, l.templateArgs(id, args), Default(defaultText))
		},
		"th": func(id string, args ...any) template.HTML {
			return l.HTML(id, l.templateArgs(id, args))
		},
		"lang":     l.Locale,
		"dir":      func() string { return string(l.Direction()) },
		"num":      func(v any, opts ...any) string { return l.Number(v, l.templateOpts("num", opts)...) },
		"percent":  func(v any, opts ...any) string { return l.Percent(v, l.templateOpts("percent", opts)...) },
		"money":    l.templateMoney,
		"unit":     func(v any, unit string, opts ...any) string { return l.Unit(v, unit, l.templateOpts("unit", opts)...) },
		"date":     func(v any, opts ...any) string { return l.Date(v, l.templateOpts("date", opts)...) },
		"time":     func(v any, opts ...any) string { return l.Time(v, l.templateOpts("time", opts)...) },
		"datetime": func(v any, opts ...any) string { return l.DateTime(v, l.templateOpts("datetime", opts)...) },
	}
}

// TemplateFuncs returns placeholders for the template functions, to parse
// templates before a Localizer exists. They render message IDs (or the
// inline default), no locale and "ltr".
func TemplateFuncs() map[string]any {
	return map[string]any{
		"t":        func(id string, _ ...any) string { return id },
		"td":       func(_, defaultText string, _ ...any) string { return defaultText },
		"th":       func(id string, _ ...any) template.HTML { return template.HTML(escapeText(id)) }, //nolint:gosec // escaped
		"lang":     func() string { return "" },
		"dir":      func() string { return string(LTR) },
		"num":      func(any, ...any) string { return "" },
		"percent":  func(any, ...any) string { return "" },
		"money":    func(any, ...any) string { return "" },
		"unit":     func(any, string, ...any) string { return "" },
		"date":     func(any, ...any) string { return "" },
		"time":     func(any, ...any) string { return "" },
		"datetime": func(any, ...any) string { return "" },
	}
}

// templateMoney is the money template function: a Money, or an amount
// followed by its currency code, then option pairs.
func (l *Localizer) templateMoney(v any, args ...any) string {
	var opts []Option
	if _, isMoney := v.(Money); !isMoney && len(args)%2 == 1 {
		currency, ok := args[0].(string)
		if !ok {
			l.reportTemplateMisuse("", fmt.Sprintf("money: want a currency code after the amount, got %T", args[0]))
		} else {
			opts = append(opts, Opt("currency", currency))
		}
		args = args[1:]
	}
	return l.Currency(v, append(opts, l.templateOpts("money", args)...)...)
}

// templateOpts turns name/value pairs into MF2 function options. Values
// may be strings, numbers or booleans. Malformed pairs are reported and
// skipped.
func (l *Localizer) templateOpts(fn string, args []any) []Option {
	opts := make([]Option, 0, len(args)/2)
	for i := 0; i < len(args); i += 2 {
		name, ok := args[i].(string)
		if !ok || i+1 == len(args) {
			l.reportTemplateMisuse("", fmt.Sprintf("%s: option %d: want name/value pairs, got %T", fn, i, args[i]))
			continue
		}
		opts = append(opts, Opt(name, fmt.Sprint(args[i+1])))
	}
	return opts
}

// templateArgs turns template arguments into Args: a single Args (or
// map[string]any), or name/value pairs. Malformed pairs are reported and
// skipped, so a template bug degrades one placeholder, not the email.
func (l *Localizer) templateArgs(id string, args []any) Args {
	if len(args) == 1 {
		if m, ok := args[0].(map[string]any); ok {
			return m
		}
	}
	out := make(Args, len(args)/2)
	for i := 0; i < len(args); i += 2 {
		name, ok := args[i].(string)
		if !ok || i+1 == len(args) {
			l.reportTemplateMisuse(id, fmt.Sprintf("template argument %d: want name/value pairs, got %T", i, args[i]))
			continue
		}
		out[name] = args[i+1]
	}
	return out
}

func (l *Localizer) reportTemplateMisuse(id, detail string) {
	e := Error{Type: ErrorFormat, Detail: detail, MessageID: id, Locale: l.Locale()}
	if ref, ok := l.c.Release(); ok {
		e.ReleaseID = ref.ID
	}
	l.c.reporter.report(e)
}
