// Package glossa is the Go runtime of Glossa: it renders localized
// messages in Go backends — transactional email, PDFs, chat messages, CLI
// output — from releases published through glossa-edge. It implements the
// delivery and runtime contract in runtimes/SPEC.md and passes its
// conformance fixtures.
//
// # Rendering
//
// Create one [Client] per project and environment, and render with the
// locale on the context or an explicit [Localizer]:
//
//	client, err := glossa.New(glossa.Config{
//		EdgeURL:     "https://edge.glossa.dev",
//		DeliveryKey: deliveryKey,
//		Environment: "production",
//		PublicKeys:  []glossa.PublicKey{signingKey},
//		Bundled:     bundledCatalogs, // fs.FS from `glossa pull --release`
//	})
//	defer client.Close()
//
//	amount := glossa.Money{Amount: glossa.MustParseDecimal("42.50"), Currency: "EUR"}
//	subject := client.T(ctx, "invoice.payment_received", glossa.Args{"amount": amount})
//	body := client.For(user.Locale).T("invoice.body", args, glossa.Default("Thanks!"))
//
// A message resolves through the release's fallback graph and is formatted
// with the locale it was found in, so plural rules and number formats match
// the language of the text. Rendering never panics and never returns an
// empty string: when no loaded locale has the message, the inline default
// ([Default]) or the message ID is rendered. [Localizer.Explain] shows how
// a message resolved; errors go to Config.OnError (or the log), rate
// limited, never to the caller.
//
// # Loading and reliability
//
// New restores the persisted last-good release from the cache directory,
// or the bundled catalogs when they are newer, so a service renders
// without the network. A background refresh revalidates the manifest
// every five minutes (with jitter) through retries and a circuit breaker.
// A new release is verified — Ed25519 manifest signature over the RFC 8785
// canonical form when keys are configured, and SHA-256 for every artifact
// — and only then activated, atomically. Until then the previous release
// keeps serving.
//
// # Staged rollout
//
// A manifest may carry a candidate release for a share of installations
// (SPEC §1.4). The cohort key decides the side: the process's installation
// id (Config.InstallationID, or a random id kept in the cache directory),
// or, on a server serving many users, a per-request key attached with
// [WithCohortKey] — set Config.PerRequestCohorts so both sides are loaded.
// A candidate that can't be activated falls back to the stable release of
// the same manifest; Config.DisableRollout ignores rollouts entirely.
// [Explanation].Rollout reports the rollout id, percent, cohort and side.
//
//	ctx = glossa.WithCohortKey(ctx, user.ID)
//	client.T(ctx, "checkout.title", nil)
//
// # Servers
//
// [Client.Middleware] resolves each request's locale with a
// [ResolverChain] (explicit → user → organization → request metadata →
// Accept-Language → source locale), canonicalizes the proposals and
// matches them with RFC 4647 Lookup. Handlers call [Client.T] with the
// request context.
//
// # Templates
//
// [Localizer.FuncMap] provides t, td, th, lang and dir for html/template
// and text/template, and the formatters num, percent, money, unit, date,
// time and datetime. Parse email templates once with [TemplateFuncs], then
// bind the recipient's locale on a clone:
//
//	var welcome = template.Must(template.New("welcome").Funcs(glossa.TemplateFuncs()).Parse(`
//	<html lang="{{lang}}" dir="{{dir}}">
//	  <h1>{{t "email.welcome.title" "name" .Name}}</h1>
//	  <p>{{td "email.welcome.body" "Your first loaf is on its way."}}</p>
//	</html>`))
//
//	func render(client *glossa.Client, u User) (string, error) {
//		tmpl := template.Must(welcome.Clone()) // never execute the base itself
//		var b strings.Builder
//		err := tmpl.Funcs(client.For(u.Locale).FuncMap()).Execute(&b, u)
//		return b.String(), err
//	}
//
// # Markup and documents
//
// T renders MF2 markup ({#b}…{/b}) as nothing. [Localizer.Parts] keeps
// it: text, markup, placeholder values and fallbacks, with the same
// resolution and fallbacks as T. On top of the parts, [Localizer.HTML]
// (the template function th) renders safe HTML, with the rules of
// @klarlabs-studio/glossa/elements: only allow-listed inline tags become elements, markup
// options are always dropped (a translation can't add a link), and other
// markup keeps only its text. [Localizer.Runs] renders text runs with
// bold, italic and underline flags, whose [Run.Style] is fpdf's SetFont
// style; the runtime itself has no PDF dependency.
//
// Documents (PDF, CSV, plain-text email) in left-to-right locales should
// render with [BidiIsolation](false): MF2 isolates every placeholder with
// U+2068/U+2069 by default, which a PDF font has no glyph for and a
// text export would carry along. Keep isolation on, the default, for HTML
// and for right-to-left locales, where it keeps a Latin name or a number
// from reordering the sentence around it.
//
//	for _, r := range l.Runs("invoice.note", args, glossa.BidiIsolation(false)) { … }
//
// Correct output contains U+00A0 and U+202F (German and French spacing)
// and CJK text, which the PDF core fonts (CP1252) can't draw: embed UTF-8
// TrueType fonts, such as Noto Sans and Noto Sans JP. The module in
// examples/pdf is a complete fpdf adapter: it renders the tax summary and
// export of testdata/documents in de, en, es, fr and ja.
//
// # Numbers, money and dates
//
// Pass amounts as [Decimal] or [Money], never float64: a Decimal keeps
// its literal and formats to exactly its digits, so a tax amount of
// 12345678901234.56 renders as "12.345.678.901.234,56 €" and not with a
// float's rounding. Money carries its ISO 4217 currency, whose CLDR
// fraction digits apply (JPY 0, EUR 2) unless the message sets its own.
// An unannotated {$total} whose argument is Money formats as :currency,
// and one whose argument is a Decimal as :number.
//
//	total := glossa.Money{Amount: glossa.MustParseDecimal("1234.50"), Currency: "EUR"}
//	l.T("invoice.total", glossa.Args{"total": total}) // "Summe: 1.234,50 €"
//
// For table cells and totals outside a sentence, [Localizer.Number],
// [Localizer.Percent], [Localizer.Currency], [Localizer.Unit],
// [Localizer.Date], [Localizer.Time] and [Localizer.DateTime] format one
// value in the active locale, with the MF2 function's options ([Opt]).
// Each formats the one-placeholder message {$value :number …} through the
// same engine as T, so a cell and a sentence never disagree. In templates
// they are num, percent, money, unit, date, time and datetime.
//
//	l.Currency(total)                                       // "1.234,50 €"
//	l.Number(glossa.MustParseDecimal("0.5"), glossa.Opt("minimumFractionDigits", "2")) // "0,50"
//	l.Date(issuedAt, glossa.Opt("length", "long"))          // "19. September 2026"
//
// # Time zones
//
// Dates and times render in UTC by default, whatever the time.Time's
// location and the server's TZ, so output never depends on where a value
// came from. [Localizer.WithTimeZone] sets a Localizer's zone, the
// [TimeZone] option one call's; both apply to the :date, :time and
// :datetime placeholders (and formatters) that don't set a timeZone
// option, which always wins.
//
//	berlin, _ := time.LoadLocation("Europe/Berlin")
//	l := client.For(customer.Locale).WithTimeZone(berlin)
//	l.T("invoice.due", glossa.Args{"due": dueAt}) // "Fällig am 19. September 2026 um 16:05"
//
// For CLI output, [EnvLocales] reads the user's locale from the POSIX
// environment and [BidiIsolation](false) keeps isolation marks out of the
// terminal.
package glossa
