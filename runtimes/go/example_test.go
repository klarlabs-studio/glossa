package glossa_test

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	glossa "go.klarlabs.de/glossa/runtimes/go"
)

// newExampleClient loads the catalogs in testdata/bundle, as a binary
// would from an embed.FS. Bidi isolation is off so the printed output is
// plain; keep it on for text that mixes scripts.
func newExampleClient() *glossa.Client {
	client, err := glossa.New(glossa.Config{
		Bundled:              os.DirFS("testdata/bundle"),
		DisableBidiIsolation: true,
		OnError:              func(glossa.Error) {}, // route to your logger or metrics
	})
	if err != nil {
		log.Fatal(err)
	}
	return client
}

func Example() {
	client := newExampleClient()
	defer client.Close()

	de := client.For("de-AT")
	fmt.Println(de.T("greeting", nil))
	// Not translated to German: falls back to the source locale.
	fmt.Println(de.T("invoice.overdue", glossa.Args{"number": "2026-042"}))
	// Not in the release yet: the inline default, else the message ID.
	fmt.Println(de.T("invoice.paid", nil, glossa.Default("Payment received")))
	// Output:
	// Hallo!
	// Invoice 2026-042 is overdue.
	// Payment received
}

// Production setup: the edge with signature verification, a persisted
// last-good release, bundled catalogs for offline starts, and errors in
// the service's log.
func ExampleNew() {
	signingKey, err := glossa.ParsePublicKey("k_2026a", os.Getenv("GLOSSA_SIGNING_KEY"))
	if err != nil {
		log.Fatal(err)
	}
	client, err := glossa.New(glossa.Config{
		EdgeURL:     "https://edge.glossa.dev",
		DeliveryKey: os.Getenv("GLOSSA_DELIVERY_KEY"),
		Environment: "production",
		PublicKeys:  []glossa.PublicKey{signingKey},
		// The output of `glossa pull --release`, e.g. embedded with
		// //go:embed glossa and narrowed with fs.Sub(files, "glossa").
		Bundled: os.DirFS("testdata/bundle"),
		OnError: func(e glossa.Error) {
			slog.Warn("glossa", "type", e.Type, "detail", e.Detail, "message_id", e.MessageID)
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()
}

type recipient struct {
	Name   string
	Locale string
	Loaves int
}

// Parse once at startup; placeholders stand in for the functions until a
// Localizer is bound.
var welcomeEmail = template.Must(template.New("welcome").Funcs(glossa.TemplateFuncs()).Parse(
	`<html lang="{{lang}}" dir="{{dir}}"><p>{{t "email.welcome.body" "loaves" .Loaves}}</p></html>`))

// A batch job renders transactional email for many recipients, each in
// their own locale.
func Example_email() {
	client := newExampleClient()
	defer client.Close()

	for _, r := range []recipient{{"Ada", "en-GB", 1}, {"Jonas", "de-AT", 3}} {
		loc := client.For(r.Locale)
		subject := loc.T("email.welcome.subject", glossa.Args{"name": r.Name})

		// Bind the recipient's locale on a clone of the unexecuted template.
		tmpl := template.Must(welcomeEmail.Clone())
		var body strings.Builder
		if err := tmpl.Funcs(loc.FuncMap()).Execute(&body, r); err != nil {
			log.Fatal(err)
		}
		fmt.Println(subject)
		fmt.Println(body.String())
	}
	// Output:
	// Welcome to Brotwerk, Ada!
	// <html lang="en" dir="ltr"><p>Your first loaf is on its way.</p></html>
	// Willkommen bei Brotwerk, Jonas!
	// <html lang="de" dir="ltr"><p>Deine 3 Brote sind unterwegs.</p></html>
}

// A CLI renders output in the user's locale, typically
// client.For(glossa.EnvLocales()...) for LANG=de_DE.UTF-8.
func Example_cli() {
	client := newExampleClient()
	defer client.Close()

	out := client.For("de_DE")
	fmt.Println(out.T("cli.sync.done", glossa.Args{"count": 1234, "seconds": 2.345}))
	// Output:
	// 1.234 Einträge in 2,3 s synchronisiert
}

// The middleware resolves each request's locale (explicit → user → org →
// metadata → Accept-Language → source) and puts it on the context, where
// T finds it.
func ExampleClient_Middleware() {
	client := newExampleClient()
	defer client.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		n, _ := glossa.NegotiationFrom(r.Context())
		fmt.Fprintf(w, "%s (%s by %s)\n", client.T(r.Context(), "greeting", nil), n.Locale, n.By)
	})
	handler := client.Middleware(glossa.ResolverChain{
		Explicit: glossa.QueryParam("lang"),
		User: func(r *http.Request) []string {
			return nil // e.g. the signed-in user's saved locale
		},
	})(mux)

	for _, target := range []string{"/", "/?lang=de-CH"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.Header.Set("Accept-Language", "ar-EG, en;q=0.8")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		fmt.Print(rec.Body.String())
	}
	// Output:
	// مرحبا! (ar by accept-language)
	// Hallo! (de by explicit)
}

func ExampleLocalizer_Explain() {
	client := newExampleClient()
	defer client.Close()

	e := client.For("de-CH").Explain("invoice.overdue")
	b, _ := json.MarshalIndent(e, "", "  ")
	fmt.Println(string(b))
	// Output:
	// {
	//   "id": "invoice.overdue",
	//   "requested": [
	//     "de-CH"
	//   ],
	//   "locale": "de",
	//   "chain": [
	//     "de",
	//     "en"
	//   ],
	//   "resolvedFrom": "en",
	//   "release": {
	//     "id": "rel_example",
	//     "version": 1
	//   },
	//   "source": "bundled",
	//   "steps": [
	//     {
	//       "locale": "de",
	//       "outcome": "missing"
	//     },
	//     {
	//       "locale": "en",
	//       "outcome": "found"
	//     }
	//   ],
	//   "rollout": null
	// }
}

// A translation's markup renders as safe HTML: only allow-listed tags
// become (attribute-free) elements, so the link in the message stays
// text. In templates, the same function is th.
func ExampleLocalizer_HTML() {
	client := newExampleClient()
	defer client.Close()

	fmt.Println(client.For("en").HTML("invoice.terms", glossa.Args{"days": 14}))
	// Output:
	// Pay within <b>14 days</b>.<br><i>Questions?</i> Ask <u>us</u>.
}

// Runs carry fpdf's font styles, so a PDF renderer needs no markup
// parser. Keep isolation marks out of left-to-right documents.
func ExampleLocalizer_Runs() {
	client := newExampleClient()
	defer client.Close()

	for _, r := range client.For("en").Runs("invoice.terms", glossa.Args{"days": 14}, glossa.BidiIsolation(false)) {
		// pdf.SetFont("NotoSans", r.Style(), 10); pdf.Write(5, r.Text)
		fmt.Printf("%-2s %q\n", r.Style(), r.Text)
	}
	// Output:
	//    "Pay within "
	// B  "14 days"
	//    ".\n"
	// I  "Questions?"
	//    " Ask "
	// U  "us"
	//    "."
}

// Table cells and totals: exact amounts, formatted like the same value in
// a sentence, in the recipient's locale and time zone.
func ExampleLocalizer_Currency() {
	client := newExampleClient()
	defer client.Close()

	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		log.Fatal(err)
	}
	de := client.For("de").WithTimeZone(berlin)
	tax := glossa.Money{Amount: glossa.MustParseDecimal("12345678901234.56"), Currency: "EUR"}
	fee := glossa.Money{Amount: glossa.NewDecimal(123456, 0), Currency: "JPY"}
	issued := time.Date(2026, 9, 19, 22, 30, 0, 0, time.UTC)

	fmt.Println(de.Currency(tax))
	fmt.Println(de.Currency(fee))
	fmt.Println(de.Number(glossa.MustParseDecimal("0.5"), glossa.Opt("minimumFractionDigits", "2")))
	fmt.Println(de.Date(issued, glossa.Opt("length", "long")))
	// Output:
	// 12.345.678.901.234,56 €
	// 123.456 ¥
	// 0,50
	// 20. September 2026
}
