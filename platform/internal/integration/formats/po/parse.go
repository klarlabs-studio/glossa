package po

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/htmlindex"

	"go.klarlabs.de/glossa/platform/internal/integration/formats"
)

// entry is one parsed PO entry.
type entry struct {
	// line is where the entry starts (msgctxt or msgid), strLine its
	// first msgstr.
	line      int
	strLine   int
	ctxt      string
	hasCtxt   bool
	id        string
	hasID     bool
	plural    string
	hasPlural bool
	strs      map[int]string
	fuzzy     bool
	extracted []string
	comments  []string
	refs      []string
}

func (e *entry) empty() bool {
	return !e.hasID && !e.hasCtxt && len(e.strs) == 0
}

var charsetPattern = regexp.MustCompile(`charset=([A-Za-z0-9_.:\-]+)`)

// decode strips a UTF-8 byte order mark and transcodes a file whose
// header declares another charset.
func decode(data []byte) ([]byte, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	m := charsetPattern.FindSubmatch(data)
	if m == nil || strings.EqualFold(string(m[1]), "utf-8") || strings.EqualFold(string(m[1]), "charset") {
		return data, nil
	}
	enc, err := htmlindex.Get(string(m[1]))
	if err != nil {
		return nil, formats.Unsupportedf("charset %q", m[1])
	}
	return enc.NewDecoder().Bytes(data)
}

type parser struct {
	cur *entry
	// appendTo extends the string a continuation line belongs to.
	appendTo func(string)
	entries  []*entry
	max      int
	line     int
}

func parse(data []byte, max int) ([]*entry, error) {
	p := &parser{cur: &entry{strs: map[int]string{}}, max: max}
	for i, raw := range bytes.Split(data, []byte("\n")) {
		p.line = i + 1
		if !utf8.Valid(raw) {
			return nil, p.errf("line is not valid UTF-8 (declare the charset in Content-Type)")
		}
		if err := p.parseLine(strings.TrimSpace(string(raw))); err != nil {
			return nil, err
		}
	}
	if err := p.finish(); err != nil {
		return nil, err
	}
	return p.entries, nil
}

func (p *parser) errf(msg string, args ...any) error {
	return &formats.Error{Format: format, Line: p.line, Column: 1, Err: formats.Invalidf(msg, args...)}
}

func (p *parser) parseLine(line string) error {
	switch {
	case line == "":
		p.appendTo = nil
		return p.finish()
	case strings.HasPrefix(line, "#~"), strings.HasPrefix(line, "#|"):
		p.appendTo = nil
		return nil
	case strings.HasPrefix(line, "#"):
		return p.comment(line)
	case strings.HasPrefix(line, `"`):
		return p.continuation(line)
	}
	return p.keyword(line)
}

// finish ends the current entry.
func (p *parser) finish() error {
	e := p.cur
	p.cur, p.appendTo = &entry{strs: map[int]string{}}, nil
	if e.empty() {
		return nil
	}
	if !e.hasID || len(e.strs) == 0 {
		return &formats.Error{Format: format, Line: e.line, Column: 1, Err: formats.Invalidf("an entry needs msgid and msgstr")}
	}
	if len(p.entries) >= p.max {
		return &formats.Error{Format: format, Line: e.line, Err: fmt.Errorf("%w: more than %d entries", formats.ErrTooLarge, p.max)}
	}
	p.entries = append(p.entries, e)
	return nil
}

func (p *parser) comment(line string) error {
	if len(p.cur.strs) > 0 {
		if err := p.finish(); err != nil {
			return err
		}
	}
	p.appendTo = nil
	body := strings.TrimSpace(line[min(2, len(line)):])
	switch {
	case strings.HasPrefix(line, "#."):
		p.cur.extracted = append(p.cur.extracted, body)
	case strings.HasPrefix(line, "#:"):
		p.cur.refs = append(p.cur.refs, strings.Fields(body)...)
	case strings.HasPrefix(line, "#,"):
		for _, flag := range strings.Split(body, ",") {
			p.cur.fuzzy = p.cur.fuzzy || strings.TrimSpace(flag) == "fuzzy"
		}
	default:
		p.cur.comments = append(p.cur.comments, strings.TrimSpace(line[1:]))
	}
	return nil
}

var keywordPattern = regexp.MustCompile(`^(msgctxt|msgid_plural|msgid|msgstr(?:\[(\d{1,2})\])?)\s*(".*)$`)

func (p *parser) keyword(line string) error {
	m := keywordPattern.FindStringSubmatch(line)
	if m == nil {
		return p.errf("unexpected line %.40q", line)
	}
	text, err := unquote(m[3])
	if err != nil {
		return p.errf("%v", err)
	}
	switch kw := m[1]; {
	case kw == "msgctxt" || kw == "msgid":
		return p.start(kw, text)
	case kw == "msgid_plural":
		return p.pluralID(text)
	}
	return p.msgstr(m[2], text)
}

// start begins msgctxt or msgid, ending the previous entry first.
func (p *parser) start(kw, text string) error {
	e := p.cur
	if e.hasID || (kw == "msgctxt" && e.hasCtxt) {
		if err := p.finish(); err != nil {
			return err
		}
		e = p.cur
	}
	if e.line == 0 {
		e.line = p.line
	}
	if kw == "msgctxt" {
		e.ctxt, e.hasCtxt = text, true
		p.appendTo = func(s string) { e.ctxt += s }
		return nil
	}
	e.id, e.hasID = text, true
	p.appendTo = func(s string) { e.id += s }
	return nil
}

func (p *parser) pluralID(text string) error {
	e := p.cur
	if !e.hasID || e.hasPlural || len(e.strs) > 0 {
		return p.errf("msgid_plural must follow msgid")
	}
	e.plural, e.hasPlural = text, true
	p.appendTo = func(s string) { e.plural += s }
	return nil
}

func (p *parser) msgstr(index, text string) error {
	e := p.cur
	if !e.hasID {
		return p.errf("msgstr without msgid")
	}
	i := 0
	if index != "" {
		i, _ = strconv.Atoi(index)
	}
	if (index != "") != e.hasPlural {
		return p.errf("plural entries use msgstr[N], others msgstr")
	}
	if _, dup := e.strs[i]; dup {
		return p.errf("msgstr[%d] given twice", i)
	}
	if e.strLine == 0 {
		e.strLine = p.line
	}
	e.strs[i] = text
	p.appendTo = func(s string) { e.strs[i] += s }
	return nil
}

func (p *parser) continuation(line string) error {
	if p.appendTo == nil {
		return p.errf("string continuation without a keyword")
	}
	text, err := unquote(line)
	if err != nil {
		return p.errf("%v", err)
	}
	p.appendTo(text)
	return nil
}

// unquote reads a C string literal.
func unquote(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", fmt.Errorf("expected a quoted string, got %.40q", s)
	}
	var b strings.Builder
	body := s[1 : len(s)-1]
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c == '"':
			return "", fmt.Errorf("unescaped quote in %.40q", s)
		case c != '\\':
			b.WriteByte(c)
			continue
		}
		n, err := unescape(&b, body[i+1:])
		if err != nil {
			return "", err
		}
		i += n
	}
	return b.String(), nil
}

// unescape writes the escape sequence at the start of s and returns how
// many bytes of s it used.
func unescape(b *strings.Builder, s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("string ends in a backslash")
	}
	if r, ok := simpleEscapes[s[0]]; ok {
		b.WriteByte(r)
		return 1, nil
	}
	switch {
	case s[0] >= '0' && s[0] <= '7':
		n := 1
		for n < 3 && n < len(s) && s[n] >= '0' && s[n] <= '7' {
			n++
		}
		v, _ := strconv.ParseUint(s[:n], 8, 8)
		b.WriteByte(byte(v))
		return n, nil
	case s[0] == 'x':
		n := 1
		for n < 3 && n < len(s) && isHex(s[n]) {
			n++
		}
		if n == 1 {
			return 0, fmt.Errorf(`\x without hex digits`)
		}
		v, _ := strconv.ParseUint(s[1:n], 16, 8)
		b.WriteByte(byte(v))
		return n, nil
	}
	return 0, fmt.Errorf("unknown escape \\%c", s[0])
}

var simpleEscapes = map[byte]byte{
	'n': '\n', 't': '\t', 'r': '\r', '\\': '\\', '"': '"', '\'': '\'', '?': '?',
	'a': '\a', 'b': '\b', 'f': '\f', 'v': '\v',
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}
