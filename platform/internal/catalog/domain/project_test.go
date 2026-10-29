package domain_test

import (
	"errors"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/catalog/domain"
	"go.klarlabs.de/glossa/platform/internal/kernel/bcp47"
	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
	"go.klarlabs.de/glossa/platform/internal/kernel/mfcontent"
	"go.klarlabs.de/glossa/platform/internal/kernel/tenancy"
)

func TestNewProject(t *testing.T) {
	p, err := domain.NewProject(tenancy.NewID(), "brotwerk", "Brotwerk", bcp47.MustParse("de"), domain.DefaultSettings(), t0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != 1 || p.SourceLocale.String() != "de" || p.Settings.DefaultSyntax != mfcontent.MF1 || !p.Settings.ReviewRequired {
		t.Errorf("project = %+v", p)
	}
	if _, err := domain.NewProject(tenancy.NewID(), "x", "", en, domain.DefaultSettings(), t0); !errors.Is(err, domain.ErrInvalidName) {
		t.Errorf("empty name: %v", err)
	}
	if _, err := domain.NewProject(tenancy.NewID(), "x", "X", bcp47.Tag{}, domain.DefaultSettings(), t0); !errors.Is(err, bcp47.ErrInvalid) {
		t.Errorf("no source locale: %v", err)
	}
	if _, err := domain.NewProject(tenancy.NewID(), "x", "X", en, domain.Settings{DefaultSyntax: "icu"}, t0); !errors.Is(err, mfcontent.ErrInvalidSyntax) {
		t.Errorf("bad settings: %v", err)
	}
}

func TestProjectChange(t *testing.T) {
	p, _ := domain.NewProject(tenancy.NewID(), "brotwerk", "Brotwerk", en, domain.DefaultSettings(), t0)
	name := "Brotwerk Web"
	settings := domain.Settings{DefaultSyntax: mfcontent.MF2}
	changed, err := p.Change(domain.ProjectChange{Name: &name, Settings: &settings}, nil, t0)
	if err != nil || !changed || p.Version != 2 || p.Name != name || p.Settings.ReviewRequired {
		t.Fatalf("change: %v %v %+v", changed, err, p)
	}
	if changed, _ := p.Change(domain.ProjectChange{Name: &name}, nil, t0); changed || p.Version != 2 {
		t.Error("no-op change bumped the version")
	}
}

func TestProjectDefaultBranch(t *testing.T) {
	p, err := domain.NewProject(tenancy.NewID(), "brotwerk", "Brotwerk", en, domain.Settings{DefaultSyntax: mfcontent.MF1}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Settings.DefaultBranch != domain.DefaultBranch {
		t.Errorf("default branch of a new project = %q, want %q", p.Settings.DefaultBranch, domain.DefaultBranch)
	}
	// Settings without a default branch keep the current one.
	trunk := domain.Settings{DefaultSyntax: mfcontent.MF1, DefaultBranch: "trunk"}
	if changed, err := p.Change(domain.ProjectChange{Settings: &trunk}, nil, t0); err != nil || !changed || p.Settings.DefaultBranch != "trunk" {
		t.Fatalf("set trunk: %v %v %+v", changed, err, p.Settings)
	}
	keep := domain.Settings{DefaultSyntax: mfcontent.MF1}
	if changed, err := p.Change(domain.ProjectChange{Settings: &keep}, nil, t0); err != nil || changed || p.Settings.DefaultBranch != "trunk" {
		t.Errorf("settings without default_branch: %v %v %+v", changed, err, p.Settings)
	}
	for _, bad := range []string{"feat/.hidden", "a b", "x..y", "@", "main.lock"} {
		s := domain.Settings{DefaultSyntax: mfcontent.MF1, DefaultBranch: domain.BranchName(bad)}
		if _, err := p.Change(domain.ProjectChange{Settings: &s}, nil, t0); !errors.Is(err, domain.ErrInvalidBranchName) {
			t.Errorf("default branch %q: %v, want ErrInvalidBranchName", bad, err)
		}
		if _, err := domain.NewProject(tenancy.NewID(), "x", "X", en, s, t0); !errors.Is(err, domain.ErrInvalidBranchName) {
			t.Errorf("new project with default branch %q: %v", bad, err)
		}
	}
}

func TestProjectCheckPolicy(t *testing.T) {
	p, err := domain.NewProject(tenancy.NewID(), "brotwerk", "Brotwerk", en, domain.DefaultSettings(), t0)
	if err != nil {
		t.Fatal(err)
	}
	// A project that has never set one reads as the documented default.
	if p.Settings.CheckPolicy != nil {
		t.Errorf("new project stores a policy: %+v", p.Settings.CheckPolicy)
	}
	if got := p.Settings.Policy(); !got.Requires("de") || !got.Fails(checkpolicy.Error) || got.Fails(checkpolicy.Warning) {
		t.Errorf("default policy = %+v", got)
	}

	known := []string{"en", "de"}
	warn := domain.Settings{DefaultSyntax: mfcontent.MF1, CheckPolicy: &checkpolicy.Policy{
		RequireComplete: []string{"de"}, MissingTranslations: checkpolicy.Warning,
	}}
	changed, err := p.Change(domain.ProjectChange{Settings: &warn}, known, t0)
	if err != nil || !changed {
		t.Fatalf("set the policy: %v %v", changed, err)
	}
	if got := p.Settings.Policy(); got.Severity("de") != checkpolicy.Warning || got.FailOn != checkpolicy.Error {
		t.Errorf("stored policy = %+v", got)
	}

	// Settings without a check policy keep the project's, as
	// default_branch does; the same policy again changes nothing.
	keep := domain.Settings{DefaultSyntax: mfcontent.MF1}
	if changed, err := p.Change(domain.ProjectChange{Settings: &keep}, known, t0); err != nil || changed {
		t.Errorf("settings without check_policy: %v %v", changed, err)
	}
	if !p.Settings.Policy().Requires("de") {
		t.Errorf("policy lost: %+v", p.Settings.Policy())
	}
	again := domain.Settings{DefaultSyntax: mfcontent.MF1, CheckPolicy: &checkpolicy.Policy{
		RequireComplete: []string{"de"}, MissingTranslations: checkpolicy.Warning, FailOn: checkpolicy.Error,
	}}
	if changed, err := p.Change(domain.ProjectChange{Settings: &again}, known, t0); err != nil || changed {
		t.Errorf("the same policy again: %v %v", changed, err)
	}

	// A settings write speaks the three base fields and nothing else,
	// so it may not delete the rules the policy document carries: those
	// are the policy API's (RFC 0005 §13 wave 3), and a policy nobody
	// meant to touch is not a policy anybody deleted.
	document := domain.Settings{DefaultSyntax: mfcontent.MF1, CheckPolicy: &checkpolicy.Policy{
		Schema: checkpolicy.Schema, Version: 7, RequireComplete: []string{"de"},
		MissingTranslations: checkpolicy.Warning,
		Rules: []checkpolicy.Rule{{
			Selector: checkpolicy.Selector{Layer: "terminology", Namespace: "legal"},
			Severity: checkpolicy.Error,
		}},
		Environments: map[string]checkpolicy.Environment{
			"production": {RequireComplete: checkpolicy.AllLocales(), RequireReview: checkpolicy.ReviewApproved},
		},
	}}
	if changed, err := p.Change(domain.ProjectChange{Settings: &document}, known, t0); err != nil || !changed {
		t.Fatalf("store the document: %v %v", changed, err)
	}
	base := domain.Settings{DefaultSyntax: mfcontent.MF1, CheckPolicy: &checkpolicy.Policy{
		RequireComplete: []string{"de", "en"}, FailOn: checkpolicy.Warning,
	}}
	if changed, err := p.Change(domain.ProjectChange{Settings: &base}, known, t0); err != nil || !changed {
		t.Fatalf("change the base: %v %v", changed, err)
	}
	got := p.Settings.Policy()
	if got.FailOn != checkpolicy.Warning || len(got.RequireComplete) != 2 {
		t.Errorf("the base did not change: %+v", got)
	}
	if got.Version != 7 || len(got.Rules) != 1 || len(got.Environments) != 1 {
		t.Errorf("the document lost its rules or its version: %+v", got)
	}
	if !got.FailsDecision(got.Decide(checkpolicy.Target{
		Layer: "terminology", Namespace: "legal", Severity: checkpolicy.Warning,
	})) {
		t.Error("the rule stopped deciding after a settings write")
	}

	// A locale the project does not have is refused.
	bad := domain.Settings{DefaultSyntax: mfcontent.MF1, CheckPolicy: &checkpolicy.Policy{RequireComplete: []string{"ja"}}}
	if _, err := p.Change(domain.ProjectChange{Settings: &bad}, known, t0); !errors.Is(err, checkpolicy.ErrUnknownLocale) {
		t.Errorf("unknown locale: %v, want ErrUnknownLocale", err)
	}
	// ... and so is a severity that is not one.
	nonsense := domain.Settings{DefaultSyntax: mfcontent.MF1, CheckPolicy: &checkpolicy.Policy{FailOn: "fatal"}}
	if _, err := p.Change(domain.ProjectChange{Settings: &nonsense}, known, t0); !errors.Is(err, checkpolicy.ErrInvalidSeverity) {
		t.Errorf("bad fail_on: %v, want ErrInvalidSeverity", err)
	}
}

func TestSlugs(t *testing.T) {
	for _, bad := range []string{"", "-a", "a-", "A", "a_b"} {
		if _, err := domain.ParseSlug(bad); !errors.Is(err, domain.ErrInvalidSlug) {
			t.Errorf("ParseSlug(%q) = %v", bad, err)
		}
	}
}

func TestApplications(t *testing.T) {
	a, err := domain.NewApplication(domain.NewProjectID(), "web", "Web shop", domain.PlatformWeb, t0)
	if err != nil || a.Version != 1 {
		t.Fatalf("new application: %v %+v", err, a)
	}
	if _, err := domain.NewApplication(domain.NewProjectID(), "tv", "TV", "tv", t0); !errors.Is(err, domain.ErrInvalidPlatform) {
		t.Errorf("bad platform: %v", err)
	}
	p := domain.PlatformAPI
	if changed, err := a.Change(domain.ApplicationChange{Platform: &p}, t0); err != nil || !changed || a.Version != 2 {
		t.Errorf("change platform: %v %v", changed, err)
	}
	if changed, _ := a.Change(domain.ApplicationChange{Platform: &p}, t0); changed {
		t.Error("no-op change reported")
	}
}
