package xmlx_test

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/text/encoding/unicode"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
	"go.klarlabs.de/glossa/platform/internal/integration/formats/internal/xmlx"
)

// readTree decodes a whole document into a tree.
func readTree(t *testing.T, doc string) (*xmlx.Node, error) {
	t.Helper()
	d := xmlx.NewDecoder(strings.NewReader(doc), "test", 1<<20)
	root, err := d.Root()
	if err != nil {
		return nil, err
	}
	return d.Tree(root)
}

func TestDecoderRefusesExternalEntities(t *testing.T) {
	docs := map[string]string{
		"xxe file":               `<?xml version="1.0"?><!DOCTYPE r [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><r>&xxe;</r>`,
		"xxe parameter entity":   `<!DOCTYPE r [<!ENTITY % p SYSTEM "http://127.0.0.1/evil.dtd"> %p;]><r/>`,
		"billion laughs":         `<!DOCTYPE lolz [<!ENTITY lol "lol"><!ENTITY lol2 "&lol;&lol;&lol;&lol;">]><r>&lol2;</r>`,
		"undeclared entity":      `<r>&nbsp;</r>`,
		"entity outside doctype": `<!ENTITY x "y"><r/>`,
	}
	for name, doc := range docs {
		t.Run(name, func(t *testing.T) {
			n, err := readTree(t, doc)
			if err == nil {
				t.Fatalf("decoded %q", n.InnerText())
			}
			var fe *formats.Error
			if !errors.As(err, &fe) || fe.Format != "test" {
				t.Errorf("err = %v, want *formats.Error", err)
			}
			if strings.Contains(err.Error(), "root:") {
				t.Errorf("error leaks resolved content: %v", err)
			}
		})
	}
}

func TestDecoderNeverFetchesExternalDTD(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `<!ENTITY secret "leaked">`)
	}))
	defer srv.Close()
	doc := `<?xml version="1.0"?><!DOCTYPE tmx SYSTEM "` + srv.URL + `/tmx14.dtd"><r>ok</r>`
	n, err := readTree(t, doc)
	if err != nil || n.InnerText() != "ok" {
		t.Fatalf("tree = %v, %v; an external DTD reference must be ignored", n, err)
	}
	withEntity := `<!DOCTYPE r SYSTEM "` + srv.URL + `/x.dtd"><r>&secret;</r>`
	if _, err := readTree(t, withEntity); err == nil {
		t.Error("entity from an external DTD resolved")
	}
	if hits.Load() != 0 {
		t.Errorf("the decoder fetched the external DTD %d times", hits.Load())
	}
}

func TestDecoderBoundsDepthAndSize(t *testing.T) {
	deep := strings.Repeat("<a>", formats.MaxDepth+1) + strings.Repeat("</a>", formats.MaxDepth+1)
	if _, err := readTree(t, deep); !errors.Is(err, formats.ErrInvalid) {
		t.Errorf("deep document err = %v", err)
	}
	d := xmlx.NewDecoder(strings.NewReader("<r>"+strings.Repeat("x", 100)+"</r>"), "test", 50)
	root, err := d.Root()
	if err == nil {
		_, err = d.Tree(root)
	}
	if !errors.Is(err, formats.ErrTooLarge) {
		t.Errorf("large document err = %v, want ErrTooLarge", err)
	}
}

func TestDecoderReportsLine(t *testing.T) {
	_, err := readTree(t, "<r>\n<a>\n</b></r>")
	var fe *formats.Error
	if !errors.As(err, &fe) || fe.Line != 3 {
		t.Errorf("err = %#v, want line 3", err)
	}
}

func TestDecoderReadsUTF16AndLatin1(t *testing.T) {
	enc := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder()
	doc, err := enc.String(`<?xml version="1.0" encoding="UTF-16"?><r lang="de">Grüße</r>`)
	if err != nil {
		t.Fatal(err)
	}
	n, err := readTree(t, doc)
	if err != nil || n.InnerText() != "Grüße" || n.Get("lang") != "de" {
		t.Fatalf("UTF-16: %v, %v", n, err)
	}
	latin1 := "<?xml version=\"1.0\" encoding=\"ISO-8859-1\"?><r>Gr\xfc\xdfe</r>"
	n, err = readTree(t, latin1)
	if err != nil || n.InnerText() != "Grüße" {
		t.Fatalf("Latin-1: %v, %v", n, err)
	}
	bom := "\xEF\xBB\xBF<r>x</r>"
	if n, err := readTree(t, bom); err != nil || n.InnerText() != "x" {
		t.Fatalf("UTF-8 BOM: %v, %v", n, err)
	}
	if _, err := readTree(t, `<?xml version="1.0" encoding="x-unknown"?><r/>`); !errors.Is(err, formats.ErrUnsupported) {
		t.Errorf("unknown encoding err = %v", err)
	}
}

func TestTreeAttributesAndText(t *testing.T) {
	n, err := readTree(t, `<r xmlns="urn:x" xml:lang="en" a="1"><b>one</b> two<!-- c --><c/></r>`)
	if err != nil {
		t.Fatal(err)
	}
	if n.Name.Space != "urn:x" || n.Lang() != "en" || n.Get("a") != "1" {
		t.Errorf("root = %+v", n)
	}
	if got := len(n.Elements()); got != 2 {
		t.Errorf("elements = %d", got)
	}
	if n.InnerText() != "one two" {
		t.Errorf("text = %q", n.InnerText())
	}
}

func TestWriterRoundTripsTextAndAttributes(t *testing.T) {
	text := "a & b < c > d \"q\" 'a'\r\n\ttab  "
	var buf bytes.Buffer
	w := xmlx.NewWriter(&buf)
	w.Decl()
	w.Open("r", "a", text, "skipped", "")
	w.Leaf("t", text)
	w.OpenInline("m")
	w.Text("x")
	w.Tag("ph", "id", "1")
	w.Start("pc", "id", "2")
	w.Text("y")
	w.End("pc")
	w.Close()
	w.Empty("e")
	w.Close()
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		A    string  `xml:"a,attr"`
		Skip *string `xml:"skipped,attr"`
		T    string  `xml:"t"`
	}
	if err := xml.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("%v\n%s", err, buf.String())
	}
	if doc.A != text || doc.T != text || doc.Skip != nil {
		t.Errorf("round trip: attr %q, text %q, skipped %v", doc.A, doc.T, doc.Skip)
	}
	want := "<m>x<ph id=\"1\"/><pc id=\"2\">y</pc></m>"
	if !strings.Contains(buf.String(), want) {
		t.Errorf("inline content not verbatim:\n%s", buf.String())
	}
}

func TestWriterRefusesCharactersXMLCannotCarry(t *testing.T) {
	for _, s := range []string{"a\x00b", "\x01", "\uFFFE", "bad\xff"} {
		w := xmlx.NewWriter(&bytes.Buffer{})
		w.Decl()
		w.Leaf("r", s)
		if err := w.Flush(); !errors.Is(err, formats.ErrInvalid) {
			t.Errorf("Leaf(%q) err = %v", s, err)
		}
	}
}
