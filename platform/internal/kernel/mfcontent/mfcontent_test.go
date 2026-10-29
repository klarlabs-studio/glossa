package mfcontent_test

import (
	"errors"
	"strings"
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
)

var en = bcp47.MustParse("en")

func TestParseMF1DerivesMetadata(t *testing.T) {
	c, err := mfcontent.Parse(mfcontent.MF1, "{count, plural, one {# item for <b>{name}</b>} other {# items}}", en)
	if err != nil {
		t.Fatal(err)
	}
	if c.Model.Type != mf.SelectMessageType {
		t.Errorf("model type = %q, want select", c.Model.Type)
	}
	names := map[string]mf.ArgumentType{}
	for _, a := range c.Arguments {
		names[a.Name] = a.Type
	}
	if len(names) != 2 || names["count"] != mf.ArgNumber || names["name"] != mf.ArgString {
		t.Errorf("arguments = %+v", c.Arguments)
	}
	if string(c.ArgumentsJSON()) == "" || string(c.MarkupJSON()) != "[]" {
		t.Errorf("markup = %s (MF1 tags are text)", c.MarkupJSON())
	}
}

func TestParseMF2Markup(t *testing.T) {
	c, err := mfcontent.Parse(mfcontent.MF2, "Hello {#b}{$name}{/b}", en)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Markup) != 2 {
		t.Errorf("markup = %+v, want open and close b", c.Markup)
	}
}

func TestSameModelIgnoresAuthoringSyntax(t *testing.T) {
	a, err := mfcontent.Parse(mfcontent.MF1, "Hello {name}", en)
	if err != nil {
		t.Fatal(err)
	}
	b, err := mfcontent.Parse(mfcontent.MF2, "Hello {$name}", en)
	if err != nil {
		t.Fatal(err)
	}
	if !a.SameModel(b) {
		t.Errorf("MF1 and MF2 of the same message differ: %s vs %s", a.ModelJSON(), b.ModelJSON())
	}
	c, _ := mfcontent.Parse(mfcontent.MF2, "Hi {$name}", en)
	if a.SameModel(c) {
		t.Error("different text compares equal")
	}
}

func TestRestoreRoundTrips(t *testing.T) {
	a, err := mfcontent.Parse(mfcontent.MF1, "{n, plural, one {# day} other {# days}}", en)
	if err != nil {
		t.Fatal(err)
	}
	b, err := mfcontent.Restore(a.Syntax, a.Text, a.ModelJSON())
	if err != nil {
		t.Fatal(err)
	}
	if !a.SameModel(b) || len(b.Arguments) != 1 || b.Text != a.Text {
		t.Errorf("restored %+v", b)
	}
	if _, err := mfcontent.Restore(mfcontent.MF2, "", []byte(`{"type":"nope"}`)); err == nil {
		t.Error("restored an invalid model")
	}
}

func TestParseRejects(t *testing.T) {
	var invalid *mfcontent.InvalidError
	if _, err := mfcontent.Parse(mfcontent.MF1, "{count, plural, one {x}", en); !errors.As(err, &invalid) || invalid.Code != mf.CodeMF1SyntaxError {
		t.Errorf("broken MF1: %v", err)
	}
	if _, err := mfcontent.Parse(mfcontent.MF2, "{{unclosed", en); !errors.As(err, &invalid) {
		t.Errorf("broken MF2: %v", err)
	}
	if _, err := mfcontent.Parse("xliff", "x", en); !errors.Is(err, mfcontent.ErrInvalidSyntax) {
		t.Errorf("unknown syntax: %v", err)
	}
	if _, err := mfcontent.Parse(mfcontent.MF2, strings.Repeat("a", mfcontent.MaxTextBytes+1), en); !errors.Is(err, mfcontent.ErrTooLong) {
		t.Errorf("long text: %v", err)
	}
}

func TestParseSyntax(t *testing.T) {
	if s, err := mfcontent.ParseSyntax("", mfcontent.MF1); err != nil || s != mfcontent.MF1 {
		t.Errorf("default = %q, %v", s, err)
	}
	if _, err := mfcontent.ParseSyntax("icu", mfcontent.MF1); !errors.Is(err, mfcontent.ErrInvalidSyntax) {
		t.Errorf("icu: %v", err)
	}
}

func TestTextLengths(t *testing.T) {
	c, err := mfcontent.Parse(mfcontent.MF1, "{n, plural, one {# Tag} other {# Tage}}", bcp47.MustParse("de"))
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range c.TextLengths() {
		if n != 4 && n != 5 {
			t.Errorf("lengths = %v", c.TextLengths())
		}
	}
}
