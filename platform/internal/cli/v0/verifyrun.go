package v0

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed verify.mjs
var verifyDriver []byte

// VerifyText is one v0.3 value to render: a key in a locale, with the
// locale as v0.3 spells it (its formatter's plural rules take it) and as
// the platform does (the runtime's locale).
type VerifyText struct {
	Key, Locale, V0Locale, Text string
}

// VerifyConfig is where the two formatters and the release come from.
type VerifyConfig struct {
	// Node is the node executable.
	Node string
	// FormatModule and RuntimeModule are the package directories of
	// @felixgeelhaar/glossa-format and @klarlabs-studio/glossa, each with its
	// built entry: dist/index.js for the formatter, dist/runtime/index.js for
	// @klarlabs-studio/glossa.
	FormatModule, RuntimeModule string
	// EdgeURL, DeliveryKey and Environment are what the runtime loads
	// the release with, exactly as a product would.
	EdgeURL, DeliveryKey, Environment string
}

// VerifyRow is one rendering and its verdict.
type VerifyRow struct {
	Key          string         `json:"key"`
	Locale       string         `json:"locale"`
	Args         map[string]any `json:"args"`
	V0           *string        `json:"v0"`
	Runtime      *string        `json:"runtime"`
	V0Error      string         `json:"v0_error,omitempty"`
	RuntimeError string         `json:"runtime_error,omitempty"`
	Verdict      string         `json:"verdict"`
	// Defect names the known v0.3 defect a known_defect row is, and
	// V0Requoted is v0.3's formatter over the text requoted the ICU way:
	// the evidence that the apostrophe is the whole difference.
	Defect     string  `json:"defect,omitempty"`
	V0Requoted *string `json:"v0_requoted,omitempty"`
}

// VerifyResult is every rendering, classified.
type VerifyResult struct {
	// Release is the release the runtime loaded.
	Release string
	Rows    []VerifyRow
	// RuntimeErrors are what the runtime reported (the first 20).
	RuntimeErrors []string
}

// Count returns how many rows have verdict.
func (r VerifyResult) Count(verdict string) int {
	n := 0
	for _, row := range r.Rows {
		if row.Verdict == verdict {
			n++
		}
	}
	return n
}

// ErrNoNode means the node executable could not be found.
var ErrNoNode = errors.New("node is not installed (or not on PATH)")

// Verify renders every text both ways — each key with the argument sets
// GenerateArgs derives from its text in every locale — and classifies
// each rendering (Classify).
func Verify(ctx context.Context, cfg VerifyConfig, texts []VerifyText) (VerifyResult, error) {
	node, err := exec.LookPath(cfg.Node)
	if err != nil {
		return VerifyResult{}, fmt.Errorf("%w: %v", ErrNoNode, err)
	}
	type verifyCase struct {
		Key      string         `json:"key"`
		Locale   string         `json:"locale"`
		V0Locale string         `json:"v0Locale"`
		Args     map[string]any `json:"args"`
		Text     string         `json:"text"`
		Requoted *string        `json:"requoted,omitempty"`
	}
	byKey := map[string]map[string]string{}
	for _, t := range texts {
		if byKey[t.Key] == nil {
			byKey[t.Key] = map[string]string{}
		}
		byKey[t.Key][t.Locale] = t.Text
	}
	sorted := append([]VerifyText(nil), texts...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Key != sorted[j].Key {
			return sorted[i].Key < sorted[j].Key
		}
		return sorted[i].Locale < sorted[j].Locale
	})
	args := map[string][]map[string]any{}
	var cases []verifyCase
	for _, t := range sorted {
		if _, ok := args[t.Key]; !ok {
			args[t.Key] = GenerateArgs(byKey[t.Key])
		}
		var requoted *string
		if rq := RequoteForV0(t.Text); rq != t.Text {
			requoted = &rq
		}
		for _, a := range args[t.Key] {
			cases = append(cases, verifyCase{Key: t.Key, Locale: t.Locale, V0Locale: t.V0Locale, Args: a, Text: t.Text, Requoted: requoted})
		}
	}

	dir, err := os.MkdirTemp("", "glossa-verify-")
	if err != nil {
		return VerifyResult{}, err
	}
	defer os.RemoveAll(dir)
	driver, inPath, outPath := filepath.Join(dir, "verify.mjs"), filepath.Join(dir, "in.json"), filepath.Join(dir, "out.json")
	in, err := json.Marshal(map[string]any{
		"formatModule": cfg.FormatModule, "runtimeModule": cfg.RuntimeModule,
		"edgeURL": cfg.EdgeURL, "deliveryKey": cfg.DeliveryKey, "environment": cfg.Environment, "cases": cases,
	})
	if err != nil {
		return VerifyResult{}, err
	}
	// The input holds the delivery key: readable by this user only.
	if err := os.WriteFile(driver, verifyDriver, 0o600); err != nil {
		return VerifyResult{}, err
	}
	if err := os.WriteFile(inPath, in, 0o600); err != nil {
		return VerifyResult{}, err
	}
	cmd := exec.CommandContext(ctx, node, driver, inPath, outPath) //nolint:gosec // G204: the node the user's PATH or --node names, running the embedded driver
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stderr, &stderr
	if err := cmd.Run(); err != nil {
		return VerifyResult{}, fmt.Errorf("the renderer failed: %v: %s", err, lastLines(stderr.String(), 6))
	}
	raw, err := os.ReadFile(outPath)
	if err != nil {
		return VerifyResult{}, err
	}
	var out struct {
		Release *string `json:"release"`
		Errors  []string
		Rows    []struct {
			V0            *string `json:"v0"`
			V0Error       string  `json:"v0Error"`
			V0Requoted    *string `json:"v0Requoted"`
			RequotedError string  `json:"requotedError"`
			Runtime       *string `json:"runtime"`
			RuntimeError  string  `json:"runtimeError"`
		} `json:"rows"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return VerifyResult{}, fmt.Errorf("the renderer wrote no result: %w", err)
	}
	if len(out.Rows) != len(cases) {
		return VerifyResult{}, fmt.Errorf("the renderer answered %d of %d renderings", len(out.Rows), len(cases))
	}
	res := VerifyResult{RuntimeErrors: out.Errors}
	if out.Release != nil {
		res.Release = *out.Release
	}
	for i, c := range cases {
		o := out.Rows[i]
		row := VerifyRow{Key: c.Key, Locale: c.Locale, Args: c.Args, V0: o.V0, Runtime: o.Runtime, V0Error: o.V0Error,
			RuntimeError: o.RuntimeError}
		if o.V0 == nil && o.V0Error == "" {
			row.V0Error = "v0.3's formatter returned nothing"
		}
		row.Verdict, row.Defect = Classify(Rendering{V0: o.V0, Runtime: o.Runtime, V0Error: row.V0Error, RuntimeError: o.RuntimeError,
			V0Requoted: o.V0Requoted, RequotedError: o.RequotedError, Requoted: c.Requoted != nil})
		if row.Verdict == VerdictKnownDefect {
			row.V0Requoted = o.V0Requoted
		}
		res.Rows = append(res.Rows, row)
	}
	return res, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " / ")
}

// VerifyTexts returns the restore's text as the --verify comparison
// reads it: every non-empty value, with the platform's spelling of its
// locale and v0.3's own. only, when set, limits the locales.
func (s Snapshot) VerifyTexts(only map[string]bool) ([]VerifyText, error) {
	canon := map[string]string{}
	for _, l := range s.Locales {
		c, err := Canonical(l.Code)
		if err != nil {
			return nil, fmt.Errorf("v0.3 locale %q is not BCP 47: %w", l.Code, err)
		}
		canon[l.Code] = c
	}
	var out []VerifyText
	for _, t := range s.Translations {
		c, ok := canon[t.Locale]
		if !ok || t.Value == "" || (only != nil && !only[c]) {
			continue
		}
		out = append(out, VerifyText{Key: t.Key, Locale: c, V0Locale: t.Locale, Text: t.Value})
	}
	return out, nil
}
