package domain_test

import (
	"testing"

	mf "go.klarlabs.de/glossa/messageformat"

	"go.klarlabs.de/glossa/platform/internal/knowledge/domain"
)

func mf2(t *testing.T, src string) mf.Message {
	t.Helper()
	m, err := mf.ParseMF2(src)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	return m
}

func mf1(t *testing.T, src, locale string) mf.Message {
	t.Helper()
	m, err := mf.ParseMF1(src, locale)
	if err != nil {
		t.Fatalf("parse %q: %v", src, err)
	}
	return m
}

func TestNormalizePlaceholdersArePositional(t *testing.T) {
	tests := []struct {
		name, src, text, signature string
		vars                       []string
	}{
		{"plain", "Save changes", "Save changes", "", nil},
		{"one variable", "Pay {$amount}", "Pay {1}", "1:string", []string{"amount"}},
		{"typed", "Pay {$amount :number} now, {$name}", "Pay {1} now, {2}", "1:number,2:string", []string{"amount", "name"}},
		{"repeated", "{$a} and {$a} and {$b}", "{1} and {1} and {2}", "1:string,2:string", []string{"a", "b"}},
		{"markup kept", "Click {#b}{$label}{/b} or {#br/}", "Click <b>{1}</b> or <br/>", "1:string", []string{"label"}},
		{"whitespace collapsed", "  Save \t  all\nchanges ", "Save all changes", "", nil},
		{"literal expression", "Version {|2.0|}", "Version 2.0", "", nil},
		{"literal braces escaped", `Use \{curly\}`, `Use \{curly\}`, "", nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n := domain.Normalize(mf2(t, tc.src))
			if n.Text != tc.text {
				t.Errorf("text = %q, want %q", n.Text, tc.text)
			}
			if n.Signature != tc.signature {
				t.Errorf("signature = %q, want %q", n.Signature, tc.signature)
			}
			if len(n.Vars) != len(tc.vars) {
				t.Fatalf("vars = %v, want %v", n.Vars, tc.vars)
			}
			for i := range tc.vars {
				if n.Vars[i] != tc.vars[i] {
					t.Errorf("vars = %v, want %v", n.Vars, tc.vars)
				}
			}
			if len(n.Hash) != 64 {
				t.Errorf("hash = %q", n.Hash)
			}
		})
	}
}

// Variable names don't matter; structure and types do.
func TestNormalizeIgnoresVariableNamesButNotTypes(t *testing.T) {
	a := domain.Normalize(mf2(t, "Pay {$amount :number}"))
	b := domain.Normalize(mf2(t, "Pay {$total :number}"))
	c := domain.Normalize(mf2(t, "Pay {$total}"))
	if a.Hash != b.Hash || a.Signature != b.Signature {
		t.Errorf("renamed variable changed the key: %+v vs %+v", a, b)
	}
	if a.Hash != c.Hash || a.Signature == c.Signature {
		t.Errorf("a type change must keep the text but change the signature: %+v vs %+v", a, c)
	}
}

func TestNormalizeSelectMessages(t *testing.T) {
	en := domain.Normalize(mf1(t, "{count, plural, one {# item} other {# items}}", "en"))
	want := ".match {1}\none {{1} item}\n* {{1} items}"
	if en.Text != want {
		t.Errorf("text = %q, want %q", en.Text, want)
	}
	if en.Signature != "1:number/plural" {
		t.Errorf("signature = %q", en.Signature)
	}
	// The same message authored in MF2 normalizes identically.
	m2 := domain.Normalize(mf2(t, ".input {$n :number}\n.match $n\none {{{$n} item}}\n* {{{$n} items}}"))
	if m2.Text != en.Text {
		t.Errorf("mf2 text = %q, want %q", m2.Text, en.Text)
	}
}

func TestNormalizeLocalsResolveToTheirArgument(t *testing.T) {
	n := domain.Normalize(mf2(t, ".local $x = {$count :number}\n{{You have {$x} new}}"))
	if n.Text != "You have {1} new" || len(n.Vars) != 1 || n.Vars[0] != "count" {
		t.Errorf("normalized = %+v", n)
	}
}

func TestAdaptVariablesRenamesByPosition(t *testing.T) {
	target := mf2(t, ".input {$amount :number}\n{{{$amount} zahlen, {$name}}}")
	unit := domain.Normalize(mf2(t, "Pay {$amount :number}, {$name}"))
	query := domain.Normalize(mf2(t, "Pay {$total :number}, {$who}"))
	adapted, complete := domain.AdaptVariables(target, unit.Vars, query.Vars)
	if !complete {
		t.Fatal("every variable has a counterpart")
	}
	got, err := mf.Stringify(adapted)
	if err != nil {
		t.Fatal(err)
	}
	if want := ".input {$total :number}\n{{{$total} zahlen, {$who}}}"; got != want {
		t.Errorf("adapted = %q, want %q", got, want)
	}
	// The original is untouched.
	if orig, _ := mf.Stringify(target); orig == got {
		t.Error("AdaptVariables modified its input")
	}
	// Swapped positions swap simultaneously.
	swapped, _ := domain.AdaptVariables(mf2(t, "{$a} {$b}"), []string{"a", "b"}, []string{"b", "a"})
	if s, _ := mf.Stringify(swapped); s != "{$b} {$a}" {
		t.Errorf("swap = %q", s)
	}
	// A variable without a counterpart keeps its name and is reported.
	partial, complete := domain.AdaptVariables(mf2(t, "{$a} {$b}"), []string{"a", "b"}, []string{"x"})
	if s, _ := mf.Stringify(partial); s != "{$x} {$b}" || complete {
		t.Errorf("partial = %q, complete = %t", s, complete)
	}
}

func TestVisibleTextDropsPlaceholders(t *testing.T) {
	got := domain.VisibleText(mf1(t, "{count, plural, one {Open your <b>workspace</b>} other {Open {count} workspaces}}", "en"))
	want := "Open your <b>workspace</b>\nOpen ￼ workspaces"
	if got != want {
		t.Errorf("visible = %q, want %q", got, want)
	}
	if got := domain.VisibleText(mf2(t, "Invite {$name} to the {#b}workspace{/b}")); got != "Invite ￼ to the workspace" {
		t.Errorf("visible = %q", got)
	}
}
