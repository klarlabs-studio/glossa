package glossa

import (
	"slices"
	"testing"
)

func TestEnvLocales(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want []string
	}{
		{map[string]string{"LANG": "de_DE.UTF-8"}, []string{"de_DE"}},
		{map[string]string{"LC_ALL": "fr_CA.UTF-8@euro", "LANG": "de_DE.UTF-8"}, []string{"fr_CA"}},
		{map[string]string{"LANGUAGE": "pt_BR:pt:en", "LANG": "de_DE.UTF-8"}, []string{"pt_BR", "pt", "en", "de_DE"}},
		{map[string]string{"LC_MESSAGES": "sv_SE", "LANG": "C.UTF-8"}, []string{"sv_SE"}},
		{map[string]string{"LANG": "C"}, nil},
		{map[string]string{"LANG": "POSIX"}, nil},
		{map[string]string{"LANGUAGE": "en", "LANG": "C"}, nil},
		{map[string]string{}, nil},
	}
	for _, tc := range cases {
		getenv := func(k string) string { return tc.env[k] }
		if got := envLocales(getenv); !slices.Equal(got, tc.want) {
			t.Errorf("envLocales(%v) = %v, want %v", tc.env, got, tc.want)
		}
	}
	_ = EnvLocales() // reads the real environment; must not panic
}
