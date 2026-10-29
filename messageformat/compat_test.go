package messageformat

import (
	"encoding/json"
	"testing"
)

// CheckCompat behaviour beyond the shared fixture (testdata/glossa/compat.json).

func TestCheckCompatReturnsEmptySliceWhenCompatible(t *testing.T) {
	msg, err := ParseMF2("Hallo {$name}")
	if err != nil {
		t.Fatal(err)
	}
	findings := CheckCompat(msg, msg, "de")
	out, err := json.Marshal(findings)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "[]" {
		t.Errorf("CheckCompat = %s, want []", out)
	}
}

func TestCheckCompatNeverPanicsOnZeroMessages(t *testing.T) {
	findings := CheckCompat(Message{}, Message{}, "de")
	if len(findings) == 0 || findings[0].Code != FindingInvalidMessage {
		t.Errorf("CheckCompat(zero, zero) = %v, want invalid-message first", findings)
	}
}
