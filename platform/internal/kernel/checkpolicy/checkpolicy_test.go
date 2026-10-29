package checkpolicy_test

import (
	"encoding/json"
	"errors"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/kernel/checkpolicy"
)

func TestRequires(t *testing.T) {
	tests := []struct {
		name   string
		policy checkpolicy.Policy
		locale string
		want   bool
	}{
		{name: "nil is every locale", locale: "de", want: true},
		{name: "empty is no locale", policy: checkpolicy.Policy{RequireComplete: []string{}}, locale: "de"},
		{name: "listed", policy: checkpolicy.Policy{RequireComplete: []string{"de", "fr"}}, locale: "fr", want: true},
		{name: "not listed", policy: checkpolicy.Policy{RequireComplete: []string{"de", "fr"}}, locale: "ja"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.policy.Requires(tc.locale); got != tc.want {
				t.Errorf("Requires(%q) = %v, want %v", tc.locale, got, tc.want)
			}
		})
	}
}

func TestFails(t *testing.T) {
	tests := []struct {
		name                   string
		failOn                 checkpolicy.Severity
		wantError, wantWarning bool
	}{
		{name: "default fails on errors", wantError: true},
		{name: "error", failOn: checkpolicy.Error, wantError: true},
		{name: "warning fails on both", failOn: checkpolicy.Warning, wantError: true, wantWarning: true},
		{name: "never fails on neither", failOn: checkpolicy.Never},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := checkpolicy.Policy{FailOn: tc.failOn}
			if got := p.Fails(checkpolicy.Error); got != tc.wantError {
				t.Errorf("Fails(error) = %v, want %v", got, tc.wantError)
			}
			if got := p.Fails(checkpolicy.Warning); got != tc.wantWarning {
				t.Errorf("Fails(warning) = %v, want %v", got, tc.wantWarning)
			}
		})
	}
}

func TestSeverity(t *testing.T) {
	tests := []struct {
		name   string
		policy checkpolicy.Policy
		locale string
		want   checkpolicy.Severity
	}{
		{name: "required locale is an error", locale: "de", want: checkpolicy.Error},
		{name: "unrequired locale warns", policy: checkpolicy.Policy{RequireComplete: []string{"de"}},
			locale: "ja", want: checkpolicy.Warning},
		{name: "missing_translations warning downgrades a required locale",
			policy: checkpolicy.Policy{MissingTranslations: checkpolicy.Warning}, locale: "de", want: checkpolicy.Warning},
		{name: "missing_translations error is the default",
			policy: checkpolicy.Policy{MissingTranslations: checkpolicy.Error}, locale: "de", want: checkpolicy.Error},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.policy.Severity(tc.locale); got != tc.want {
				t.Errorf("Severity(%q) = %q, want %q", tc.locale, got, tc.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	known := []string{"en", "de", "fr"}
	tests := []struct {
		name    string
		policy  checkpolicy.Policy
		known   []string
		want    checkpolicy.Policy
		wantErr error
	}{
		{
			name: "the zero policy is the default, spelled out", known: known,
			want: checkpolicy.Policy{FailOn: checkpolicy.Error, MissingTranslations: checkpolicy.Error},
		},
		{
			name:   "a named list",
			policy: checkpolicy.Policy{RequireComplete: []string{"de"}, FailOn: checkpolicy.Warning}, known: known,
			want: checkpolicy.Policy{RequireComplete: []string{"de"}, FailOn: checkpolicy.Warning,
				MissingTranslations: checkpolicy.Error},
		},
		{
			name:   "an empty list is none",
			policy: checkpolicy.Policy{RequireComplete: []string{}}, known: known,
			want: checkpolicy.Policy{RequireComplete: []string{}, FailOn: checkpolicy.Error,
				MissingTranslations: checkpolicy.Error},
		},
		{
			name:   "never fails",
			policy: checkpolicy.Policy{FailOn: checkpolicy.Never}, known: known,
			want: checkpolicy.Policy{FailOn: checkpolicy.Never, MissingTranslations: checkpolicy.Error},
		},
		{
			name:   "an unknown locale",
			policy: checkpolicy.Policy{RequireComplete: []string{"de", "ja"}}, known: known,
			wantErr: checkpolicy.ErrUnknownLocale,
		},
		{
			name:   "no known locales skips the membership check",
			policy: checkpolicy.Policy{RequireComplete: []string{"ja"}},
			want: checkpolicy.Policy{RequireComplete: []string{"ja"}, FailOn: checkpolicy.Error,
				MissingTranslations: checkpolicy.Error},
		},
		{
			name:   "fail_on is not a severity",
			policy: checkpolicy.Policy{FailOn: "fatal"}, known: known,
			wantErr: checkpolicy.ErrInvalidSeverity,
		},
		{
			name:   "missing_translations can't be never",
			policy: checkpolicy.Policy{MissingTranslations: checkpolicy.Never}, known: known,
			wantErr: checkpolicy.ErrInvalidSeverity,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.policy.Validate(tc.known)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(tc.want) {
				t.Errorf("policy = %+v, want %+v", got, tc.want)
			}
			// Equal has to see nil and empty apart: they are opposites.
			if (got.RequireComplete == nil) != (tc.want.RequireComplete == nil) {
				t.Errorf("require_complete nil-ness = %v, want %v", got.RequireComplete == nil, tc.want.RequireComplete == nil)
			}
		})
	}
}

func TestJSONRoundTrip(t *testing.T) {
	tests := []struct {
		name   string
		policy checkpolicy.Policy
		want   string
	}{
		{name: "every locale", policy: checkpolicy.Policy{}, want: `{"require_complete":null}`},
		{name: "no locale", policy: checkpolicy.Policy{RequireComplete: []string{}}, want: `{"require_complete":[]}`},
		{
			name:   "a list",
			policy: checkpolicy.Policy{RequireComplete: []string{"de"}, FailOn: checkpolicy.Never, MissingTranslations: checkpolicy.Warning},
			want:   `{"require_complete":["de"],"fail_on":"never","missing_translations":"warning"}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.policy)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tc.want {
				t.Fatalf("json = %s, want %s", b, tc.want)
			}
			var back checkpolicy.Policy
			if err := json.Unmarshal(b, &back); err != nil {
				t.Fatal(err)
			}
			if !back.Equal(tc.policy) || (back.RequireComplete == nil) != (tc.policy.RequireComplete == nil) {
				t.Errorf("round trip = %+v, want %+v", back, tc.policy)
			}
		})
	}
}
