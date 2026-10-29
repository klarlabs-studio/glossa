package etag_test

import (
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/etag"
)

func TestRoundTrip(t *testing.T) {
	if got := etag.Format(7); got != `"7"` {
		t.Errorf("Format = %s", got)
	}
	for in, want := range map[string]int{`"7"`: 7, ` W/"12" `: 12} {
		if v, ok := etag.Parse(in); !ok || v != want {
			t.Errorf("Parse(%s) = %d, %v", in, v, ok)
		}
	}
	for _, bad := range []string{``, `7`, `"0"`, `"-1"`, `"x"`, `"`, `*`} {
		if _, ok := etag.Parse(bad); ok {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
}

func TestParseUnsavedAcceptsZero(t *testing.T) {
	for in, want := range map[string]int{`"0"`: 0, `W/"0"`: 0, `"3"`: 3} {
		if v, ok := etag.ParseUnsaved(in); !ok || v != want {
			t.Errorf("ParseUnsaved(%s) = %d, %v", in, v, ok)
		}
	}
	for _, bad := range []string{``, `0`, `"-1"`, `"x"`, `*`} {
		if _, ok := etag.ParseUnsaved(bad); ok {
			t.Errorf("ParseUnsaved(%q) accepted", bad)
		}
	}
}
