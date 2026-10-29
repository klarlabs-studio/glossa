package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"
)

// The vocabulary's fixture tables (RFC 0006 §2.4: "each [primitive] is
// a few lines over an existing port, has a fixture table, and names
// nothing about any organisation"). They are JSON so that the API and
// Studio's editor can be tested against the same cases later.

type subjectFixture struct {
	Locale      string         `json:"locale"`
	Namespace   string         `json:"namespace"`
	ReviewState string         `json:"review_state"`
	Origin      string         `json:"origin"`
	Author      string         `json:"author"`
	Band        string         `json:"band"`
	Findings    []FindingCount `json:"findings"`
	Approvers   []string       `json:"approvers"`
	TMMatch     float64        `json:"tm_match"`
	Required    int            `json:"required"`
}

type triggerFixture struct {
	Event       string   `json:"event"`
	Actor       string   `json:"actor"`
	Permissions []string `json:"permissions"`
}

type guardFixtures struct {
	Accepted []struct {
		Use    string          `json:"use"`
		Params json.RawMessage `json:"params"`
		Cases  []struct {
			Name    string         `json:"name"`
			Subject subjectFixture `json:"subject"`
			Trigger triggerFixture `json:"trigger"`
			Want    bool           `json:"want"`
		} `json:"cases"`
	} `json:"accepted"`
	Refused []struct {
		Use    string          `json:"use"`
		Params json.RawMessage `json:"params"`
	} `json:"refused"`
}

type actionFixtures struct {
	Accepted []struct {
		Use    string          `json:"use"`
		Params json.RawMessage `json:"params"`
	} `json:"accepted"`
	Refused []struct {
		Use    string          `json:"use"`
		Params json.RawMessage `json:"params"`
	} `json:"refused"`
}

func readFixture(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func TestGuardFixtures(t *testing.T) {
	var fx guardFixtures
	readFixture(t, "testdata/vocabulary/guards.json", &fx)
	trueSeen, falseSeen, refusedSeen := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, g := range fx.Accepted {
		p, ok := guardPrimitives[g.Use]
		if !ok {
			t.Fatalf("fixture for unknown guard %q", g.Use)
		}
		fn, _, err := p.compile(g.Params)
		if err != nil {
			t.Fatalf("%s %s: refused: %v", g.Use, g.Params, err)
		}
		for _, c := range g.Cases {
			s := c.Subject
			step := Step{
				Subject: Subject{Kind: SubjectTranslation, Locale: s.Locale, Namespace: s.Namespace, ReviewState: s.ReviewState,
					Origin: s.Origin, Author: s.Author, Band: s.Band, Findings: s.Findings, Approvers: s.Approvers, TMMatch: s.TMMatch,
					Required: s.Required},
				Trigger: Trigger{Event: EventName(c.Trigger.Event), Actor: c.Trigger.Actor, Permissions: c.Trigger.Permissions},
			}
			if got := fn(step); got != c.Want {
				t.Errorf("%s %s / %s: got %v, want %v", g.Use, g.Params, c.Name, got, c.Want)
			}
			if c.Want {
				trueSeen[g.Use] = true
			} else {
				falseSeen[g.Use] = true
			}
		}
	}
	for _, r := range fx.Refused {
		p, ok := guardPrimitives[r.Use]
		if !ok {
			t.Fatalf("fixture for unknown guard %q", r.Use)
		}
		if _, _, err := p.compile(r.Params); !errors.Is(err, ErrParams) {
			t.Errorf("%s %s: err = %v, want ErrParams", r.Use, r.Params, err)
		}
		refusedSeen[r.Use] = true
	}
	// Every guard has a table that shows it holding, not holding, and
	// refusing parameters: a primitive without one is not in the
	// vocabulary yet.
	for _, use := range GuardPrimitives() {
		if !trueSeen[use] || !falseSeen[use] || !refusedSeen[use] {
			t.Errorf("guard %s needs fixtures where it holds, where it does not, and parameters it refuses", use)
		}
	}
}

func TestActionFixtures(t *testing.T) {
	var fx actionFixtures
	readFixture(t, "testdata/vocabulary/actions.json", &fx)
	accepted, refused := map[string]bool{}, map[string]bool{}
	for _, a := range fx.Accepted {
		p, ok := actionPrimitives[a.Use]
		if !ok {
			t.Fatalf("fixture for unknown action %q", a.Use)
		}
		params, err := p.compile(a.Params)
		if err != nil {
			t.Errorf("%s %s: refused: %v", a.Use, a.Params, err)
			continue
		}
		// What the runner will be handed reads back as what was written.
		out, err := json.Marshal(params)
		if err != nil {
			t.Fatalf("%s: marshal: %v", a.Use, err)
		}
		if !jsonEqual(t, out, a.Params) {
			t.Errorf("%s: params read back as %s, want %s", a.Use, out, a.Params)
		}
		accepted[a.Use] = true
	}
	for _, r := range fx.Refused {
		p, ok := actionPrimitives[r.Use]
		if !ok {
			t.Fatalf("fixture for unknown action %q", r.Use)
		}
		if _, err := p.compile(r.Params); !errors.Is(err, ErrParams) {
			t.Errorf("%s %s: err = %v, want ErrParams", r.Use, r.Params, err)
		}
		refused[r.Use] = true
	}
	for _, use := range ActionPrimitives() {
		if !accepted[use] || !refused[use] {
			t.Errorf("action %s needs fixtures it accepts and parameters it refuses", use)
		}
	}
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return string(xa) == string(ya)
}
