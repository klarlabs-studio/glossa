package capture

import "testing"

func TestRefuse(t *testing.T) {
	s := func(v string) *string { return &v }
	j := Job{URL: "http://localhost:4173/?lang=de", Locale: "de"}
	for _, tc := range []struct {
		name string
		st   pageStatus
		code string
	}{
		{"preview in the locale", pageStatus{1, []*string{s("preview")}, []*string{s("de")}}, ""},
		{"one island in the locale", pageStatus{2, []*string{s("preview"), s("pr-7")}, []*string{s("ar"), s("DE")}}, ""},
		{"no runtime", pageStatus{0, nil, nil}, "no_runtime"},
		{"production", pageStatus{1, []*string{s("production")}, []*string{s("de")}}, "production_page"},
		{"production island", pageStatus{2, []*string{s("preview"), s("production")}, []*string{s("de"), s("de")}}, "production_page"},
		{"no release", pageStatus{1, []*string{nil}, []*string{nil}}, "environment_unknown"},
		{"malformed", pageStatus{2, []*string{s("preview")}, []*string{s("de")}}, "environment_unknown"},
		{"other locale", pageStatus{1, []*string{s("preview")}, []*string{s("en")}}, "locale_mismatch"},
	} {
		r := refuse(j, tc.st)
		switch {
		case tc.code == "" && r != nil:
			t.Errorf("%s: refused: %v", tc.name, r)
		case tc.code != "" && (r == nil || r.Code != tc.code):
			t.Errorf("%s: refusal = %+v, want %s", tc.name, r, tc.code)
		}
	}
}
