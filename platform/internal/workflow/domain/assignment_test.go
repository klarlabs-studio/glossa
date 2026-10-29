package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"go.klarlabs.de/glossa/platform/internal/workflow/domain"
)

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

func newAssignment(t *testing.T, due *time.Time) domain.Assignment {
	t.Helper()
	msg := uuid.New()
	a, err := domain.NewAssignment(uuid.New(), []domain.Unit{{Message: msg, Locale: "de"}, {Message: msg, Locale: "DE"}},
		domain.VendorAssignee(uuid.New()), "translations.write", due, "person:"+uuid.NewString(), t0)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestNewAssignment(t *testing.T) {
	a := newAssignment(t, nil)
	if a.State != domain.AssignmentOpen || a.Version != 1 || len(a.Units) != 1 || a.Units[0].Locale != "de" {
		t.Fatalf("a = %+v: want open, version 1, one canonical unit", a)
	}

	project, msg, by := uuid.New(), uuid.New(), "person:"+uuid.NewString()
	unit := []domain.Unit{{Message: msg, Locale: "de"}}
	past := t0.Add(-time.Hour)
	bad := map[string]func() error{
		"no units": func() error {
			_, err := domain.NewAssignment(project, nil, domain.RoleAssignee("translator"), "translations.write", nil, by, t0)
			return err
		},
		"bad locale": func() error {
			_, err := domain.NewAssignment(project, []domain.Unit{{Message: msg, Locale: "not a tag"}}, domain.RoleAssignee("translator"), "translations.write", nil, by, t0)
			return err
		},
		"unknown role": func() error {
			_, err := domain.NewAssignment(project, unit, domain.RoleAssignee("legal"), "translations.write", nil, by, t0)
			return err
		},
		"group without id": func() error {
			_, err := domain.NewAssignment(project, unit, domain.Assignee{Kind: domain.AssigneeGroup}, "translations.write", nil, by, t0)
			return err
		},
		"bad permission": func() error {
			_, err := domain.NewAssignment(project, unit, domain.RoleAssignee("translator"), "write", nil, by, t0)
			return err
		},
		"due in the past": func() error {
			_, err := domain.NewAssignment(project, unit, domain.RoleAssignee("translator"), "translations.write", &past, by, t0)
			return err
		},
		"too many units": func() error {
			many := make([]domain.Unit, domain.MaxAssignmentUnits+1)
			for i := range many {
				many[i] = domain.Unit{Message: uuid.New(), Locale: "de"}
			}
			_, err := domain.NewAssignment(project, many, domain.RoleAssignee("translator"), "translations.write", nil, by, t0)
			return err
		},
	}
	for name, fn := range bad {
		if err := fn(); !errors.Is(err, domain.ErrInvalidAssignment) {
			t.Errorf("%s: err = %v, want ErrInvalidAssignment", name, err)
		}
	}
}

func TestAssignmentStateMachine(t *testing.T) {
	by := "person:" + uuid.NewString()
	type step struct {
		name string
		do   func(*domain.Assignment) error
		want domain.AssignmentState
		err  error
	}
	accept := func(a *domain.Assignment) error { return a.Accept(t0) }
	complete := func(a *domain.Assignment) error { return a.Complete(by, t0) }
	decline := func(a *domain.Assignment) error { return a.Decline(by, "no capacity", t0) }
	cases := map[string][]step{
		"open → accepted → done": {
			{"accept", accept, domain.AssignmentAccepted, nil},
			{"accept again", accept, domain.AssignmentAccepted, domain.ErrAssignmentState},
			{"complete", complete, domain.AssignmentDone, nil},
			{"decline a done one", decline, domain.AssignmentDone, domain.ErrAssignmentState},
			{"complete again", complete, domain.AssignmentDone, domain.ErrAssignmentState},
		},
		"open → done": {
			{"complete", complete, domain.AssignmentDone, nil},
			{"accept a done one", accept, domain.AssignmentDone, domain.ErrAssignmentState},
		},
		"accepted → declined": {
			{"accept", accept, domain.AssignmentAccepted, nil},
			{"decline", decline, domain.AssignmentDeclined, nil},
			{"complete a declined one", complete, domain.AssignmentDeclined, domain.ErrAssignmentState},
		},
	}
	for name, steps := range cases {
		t.Run(name, func(t *testing.T) {
			a := newAssignment(t, nil)
			for _, s := range steps {
				before := a.Version
				err := s.do(&a)
				if !errors.Is(err, s.err) {
					t.Fatalf("%s: err = %v, want %v", s.name, err, s.err)
				}
				if a.State != s.want {
					t.Fatalf("%s: state %s, want %s", s.name, a.State, s.want)
				}
				if (err == nil) != (a.Version == before+1) {
					t.Fatalf("%s: version %d → %d", s.name, before, a.Version)
				}
			}
		})
	}

	t.Run("a declined assignment keeps its reason and who declined", func(t *testing.T) {
		a := newAssignment(t, nil)
		if err := a.Decline(by, "  no capacity ", t0); err != nil {
			t.Fatal(err)
		}
		if a.Reason != "no capacity" || a.ClosedBy != by || a.ClosedAt == nil {
			t.Fatalf("a = %+v", a)
		}
	})
}

func TestAssignmentExpires(t *testing.T) {
	due := t0.Add(72 * time.Hour)
	a := newAssignment(t, &due)
	if err := a.Expire("system:x", due.Add(-time.Minute)); !errors.Is(err, domain.ErrNotDue) {
		t.Fatalf("expire before due: err = %v", err)
	}
	if err := a.Expire("system:x", due); err != nil || a.State != domain.AssignmentExpired {
		t.Fatalf("expire at due: %v, state %s", err, a.State)
	}
	undated := newAssignment(t, nil)
	if err := undated.Expire("system:x", t0.Add(1000*time.Hour)); !errors.Is(err, domain.ErrNotDue) {
		t.Fatalf("an assignment without a due date never expires: err = %v", err)
	}
}

func TestAssignmentCoverage(t *testing.T) {
	a := newAssignment(t, nil)
	if !a.CoversAt(t0) {
		t.Fatal("an open assignment covers its units")
	}
	_ = a.Accept(t0)
	if !a.CoversAt(t0.Add(time.Hour)) {
		t.Fatal("an accepted assignment covers its units")
	}
	done := t0.Add(2 * time.Hour)
	_ = a.Complete("person:x", done)
	if !a.CoversAt(done.Add(domain.CoverageWindow)) {
		t.Fatal("a completed assignment covers its units for 30 days")
	}
	if a.CoversAt(done.Add(domain.CoverageWindow + time.Second)) {
		t.Fatal("a completed assignment stops covering after 30 days")
	}

	declined := newAssignment(t, nil)
	_ = declined.Decline("person:x", "", t0)
	if declined.CoversAt(t0) {
		t.Fatal("a declined assignment covers nothing")
	}
}

func TestAssigneeSpelling(t *testing.T) {
	for _, a := range []domain.Assignee{
		domain.MemberAssignee(uuid.New()), domain.GroupAssignee(uuid.New()),
		domain.VendorAssignee(uuid.New()), domain.RoleAssignee("reviewer"),
	} {
		got, err := domain.ParseAssignee(a.String())
		if err != nil || got != a {
			t.Errorf("ParseAssignee(%q) = %+v, %v", a.String(), got, err)
		}
	}
	for _, s := range []string{"", "member", "member:x", "role:legal", "team:" + uuid.NewString()} {
		if _, err := domain.ParseAssignee(s); err == nil {
			t.Errorf("ParseAssignee(%q) accepted", s)
		}
	}
}

// The largest batch §9.6 allows fits createAssignment's body limit at the
// longest key and locale the contract accepts.
func TestTheLargestAssignmentFitsItsBodyLimit(t *testing.T) {
	const unit = len(`{"message":"","locale":""},`) + 200 + 35
	if body := domain.MaxAssignmentUnits*unit + 1024; body > domain.MaxAssignmentBodyBytes {
		t.Fatalf("%d units need %d bytes, over the %d-byte limit", domain.MaxAssignmentUnits, body, domain.MaxAssignmentBodyBytes)
	}
}
