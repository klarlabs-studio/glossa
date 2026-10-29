// Command pdf renders the Go runtime's document fixtures
// (runtimes/go/testdata/documents: a tax summary and a training export)
// to PDF with github.com/go-pdf/fpdf, in de, en, es, fr and ja. It is the
// fpdf adapter RFC 0004 §7.2 promises: the runtime formats, this module
// draws, and the runtime itself never depends on fpdf.
//
//	go run . -out /tmp/documents
//
// The adapter needs three things from the runtime:
//
//   - Localizer.Runs for paragraphs with markup: each run's Style() is
//     fpdf's SetFont style ("B", "I", "U" combined).
//   - Localizer.T, Currency, Number, Percent, Unit and Date for table
//     cells, with amounts as glossa.Decimal, so no amount is a float.
//   - Bidi isolation off (Config.DisableBidiIsolation) for left-to-right
//     documents, so no isolation characters reach the font.
//
// # Fonts
//
// Correct output holds U+00A0 (German and French), U+202F (French
// grouping) and Japanese, which fpdf's core fonts and their CP1252
// translator can't draw, so the example embeds UTF-8 TrueType fonts
// through AddUTF8FontFromBytes: Noto Sans for de, en, es and fr, and Noto
// Sans JP for ja. CJK has no italic, so Noto Sans JP's upright faces
// serve the italic styles too.
//
// The fonts in fonts/ are subsets, committed with their SIL Open Font
// License 1.1 texts (OFL-NotoSans.txt, OFL-NotoSansJP.txt), which allow
// subsetting and redistribution; the Reserved Font Name "Source" of Noto
// Sans JP isn't used. fonts/build.sh makes them from google/fonts at a
// pinned revision, verifying each download's SHA-256. Committed subsets
// (about 390 kB) rather than the originals (4.4 MB for Noto Sans' two
// variable fonts, 9.6 MB for Noto Sans JP, and fpdf takes static fonts)
// or a download at test time keep the test hermetic and CI free of
// network access and caches. Noto Sans keeps whole Latin ranges; Noto
// Sans JP keeps kana, CJK punctuation, full-width forms and the kanji of
// the ja fixture, and TestFontsCoverDocuments fails if a document draws a
// character the fonts lack. A product embeds the full fonts instead.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	glossa "go.klarlabs.de/glossa/runtimes/go"
)

// locales are the fixture's locales.
var locales = []string{"de", "en", "es", "fr", "ja"}

func main() {
	docs := flag.String("docs", "../../testdata/documents", "the document fixtures: bundle/ and data/")
	out := flag.String("out", ".", "directory for the PDFs")
	flag.Parse()
	if err := run(*docs, *out); err != nil {
		log.Fatal(err)
	}
}

func run(docs, out string) error {
	var failed atomic.Bool
	client, err := newClient(docs, func(e glossa.Error) {
		log.Printf("glossa: %s %s (%s): %s", e.Type, e.MessageID, e.Locale, e.Detail)
		failed.Store(true)
	})
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	tax, export, err := loadData(docs)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	for _, locale := range locales {
		l, err := localizer(client, locale)
		if err != nil {
			return err
		}
		for _, doc := range renderDocuments(l, tax, export, true) {
			path := filepath.Join(out, doc.name+"."+locale+".pdf")
			if err := doc.pdf.OutputFileAndClose(path); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			fmt.Println(path)
		}
	}
	if failed.Load() {
		return errors.New("the documents rendered with errors")
	}
	return nil
}

// newClient loads the fixture's release bundle, as a service would from
// an embed.FS, with bidi isolation off for left-to-right documents.
func newClient(docs string, onError func(glossa.Error)) (*glossa.Client, error) {
	return glossa.New(glossa.Config{
		Bundled:              os.DirFS(filepath.Join(docs, "bundle")),
		DisableBidiIsolation: true,
		OnError:              onError,
	})
}

// localizer renders for locale, with dates in Europe/Berlin unless a
// placeholder names its own zone.
func localizer(client *glossa.Client, locale string) (*glossa.Localizer, error) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		return nil, err
	}
	return client.For(locale).WithTimeZone(berlin), nil
}
