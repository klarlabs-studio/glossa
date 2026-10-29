package glossa

import (
	"os"
	"strings"
)

// EnvLocales returns the user's preferred locales from the POSIX
// environment, for CLI output: LANGUAGE (a colon-separated list) followed
// by the first of LC_ALL, LC_MESSAGES and LANG, with codesets and
// modifiers removed (`de_DE.UTF-8` → `de_DE`). Like gettext, it returns
// nil for the C and POSIX locales. Pass the result to Client.For.
func EnvLocales() []string {
	return envLocales(os.Getenv)
}

func envLocales(getenv func(string) string) []string {
	effective := ""
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := getenv(name); v != "" {
			effective = posixLocale(v)
			break
		}
	}
	if effective == "" || effective == "C" || effective == "POSIX" {
		return nil
	}
	var out []string
	for _, l := range strings.Split(getenv("LANGUAGE"), ":") {
		if l = posixLocale(l); l != "" {
			out = append(out, l)
		}
	}
	return append(out, effective)
}

// posixLocale strips the codeset and modifier: `sr_RS.UTF-8@latin` → `sr_RS`.
func posixLocale(v string) string {
	v, _, _ = strings.Cut(v, "@")
	v, _, _ = strings.Cut(v, ".")
	return strings.TrimSpace(v)
}
