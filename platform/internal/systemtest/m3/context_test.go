//go:build system

package m3_test

import (
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.klarlabs.de/glossa/platform/internal/systemtest/m3/fixture"
)

// appDir is the generated fixture application.
func appDir() string { return filepath.Join("testdata", "app") }

// setup creates the organization, the project with its five locales and
// the application, and a CI token with `write` scope — what a product's
// CI would hold as a secret until the OIDC exchange replaces it.
func (s *scenario) setup() {
	t := s.t
	s.owner = s.d.signIn(t, "lena@brotwerk.example")
	var org struct{ ID string }
	s.owner.do(http.MethodPost, "/v1/tenants", map[string]string{"slug": "brotwerk", "name": "Brotwerk"}, http.StatusCreated, &org)
	s.tenant = org.ID
	var project struct{ ID string }
	s.owner.do(http.MethodPost, s.tenantPath("/projects"),
		map[string]any{"slug": "shop", "name": "Brotwerk Shop", "source_locale": s.f.SourceLocale}, http.StatusCreated, &project)
	s.project = project.ID
	for _, l := range s.f.Locales {
		s.owner.do(http.MethodPost, s.projectPath("/locales"), map[string]string{"code": l}, http.StatusCreated, nil)
	}
	var app struct{ ID string }
	s.owner.do(http.MethodPost, s.projectPath("/applications"),
		map[string]string{"slug": fixture.Application, "name": "Shop", "platform": "web"}, http.StatusCreated, &app)
	s.application = app.ID

	var token struct {
		Secret string `json:"secret"`
	}
	s.owner.do(http.MethodPost, s.tenantPath("/tokens"),
		map[string]any{"name": "ci", "scopes": []string{"write"}}, http.StatusCreated, &token)
	s.ciToken = token.Secret
	s.ci = &runner{t: t, dir: appDir(), env: map[string]string{
		"GLOSSA_SERVER":  s.d.base,
		"GLOSSA_TENANT":  s.tenant,
		"GLOSSA_PROJECT": s.project,
		"GLOSSA_TOKEN":   s.ciToken,
	}}
}

// pushItem is one message or translation a push touched.
type pushItem struct {
	Key    string `json:"key"`
	Locale string `json:"locale"`
	Status string `json:"status"`
	Error  *struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	} `json:"error"`
}

// pushJSON is `glossa push --json`.
type pushJSON struct {
	Summary      map[string]int `json:"summary"`
	Messages     []pushItem     `json:"messages"`
	Translations []pushItem     `json:"translations"`
	Branch       *struct {
		Name     string   `json:"name"`
		State    string   `json:"state"`
		NewKeys  []string `json:"new_keys"`
		PRNumber int      `json:"pr_number"`
	} `json:"branch"`
}

// pushCatalog pushes the shop's source and its four translations from
// the default branch, the way the product's CI does after a merge.
func (s *scenario) pushCatalog() {
	t := s.t
	var out pushJSON
	s.ci.ok(&out, "push", "--translations")
	created := 0
	for _, it := range out.Messages {
		if it.Status == "created" {
			created++
		}
	}
	want := len(s.f.Messages) * len(s.f.Locales) // the four targets
	if created != len(s.f.Messages) || len(out.Translations) != want {
		t.Fatalf("push created %d messages and %d translations, want %d and %d (%v)",
			created, len(out.Translations), len(s.f.Messages), want, out.Summary)
	}
	s.pushed = out.Summary
	s.pushedMessages, s.pushedTranslations = created, len(out.Translations)
	// Every message is active on the default branch.
	type message struct {
		Key   string `json:"key"`
		State string `json:"state"`
	}
	active := list[message](s.owner, s.projectPath("/messages"), url.Values{"state": {"active"}})
	if len(active) != len(s.f.Messages) {
		t.Fatalf("%d active messages, want %d", len(active), len(s.f.Messages))
	}
}

// contextBuild is one upload of usages.
type contextBuild struct {
	ID            string `json:"id"`
	ApplicationID string `json:"application_id"`
	Commit        string `json:"commit"`
	Branch        string `json:"branch"`
	OnDefault     bool   `json:"on_default_branch"`
	Source        string `json:"source"`
	Tool          struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"tool"`
	Usages      int `json:"usages"`
	UnknownKeys int `json:"unknown_keys"`
}

type contextPushJSON struct {
	Source   string       `json:"source"`
	Replayed bool         `json:"replayed"`
	Build    contextBuild `json:"build"`
}

type extractJSON struct {
	Usages []struct {
		Key       string `json:"key"`
		File      string `json:"file"`
		Line      int    `json:"line"`
		Component string `json:"component"`
		Kind      string `json:"kind"`
	} `json:"usages"`
}

// uploadUsages sends what the build and the extractor saw: the bundler
// plugin's document through `glossa context push`, and the Go and
// template side through `glossa extract --upload` (RFC 0004 §6.3).
func (s *scenario) uploadUsages() {
	t := s.t
	var pushed contextPushJSON
	s.ci.ok(&pushed, "context", "push", fixture.FileUsages)
	if pushed.Source != "plugin" || pushed.Replayed || pushed.Build.UnknownKeys != 0 ||
		pushed.Build.Usages != len(s.f.Messages) || !pushed.Build.OnDefault {
		t.Fatalf("context push = %+v, want %d plugin usages on the default branch and no unknown key",
			pushed, len(s.f.Messages))
	}
	s.pluginBuild = pushed.Build

	// Uploading the same document again stores nothing.
	var again contextPushJSON
	s.ci.ok(&again, "context", "push", fixture.FileUsages)
	if !again.Replayed || again.Build.ID != pushed.Build.ID {
		t.Errorf("the same usages document was stored twice: %+v", again)
	}

	var extracted extractJSON
	s.ci.ok(&extracted, "extract", "--upload", "--commit", s.f.Commit, "--branch", s.f.Branch)
	if len(extracted.Usages) != s.f.GoUsages() {
		t.Fatalf("extract found %d server-side usages, the fixture places %d", len(extracted.Usages), s.f.GoUsages())
	}
	s.extracted = len(extracted.Usages)

	builds := list[contextBuild](s.owner, s.projectPath("/context-builds"), url.Values{"application": {fixture.Application}})
	if len(builds) != 2 {
		t.Fatalf("%d builds, want the plugin's and the extractor's", len(builds))
	}
	for _, b := range builds {
		if b.Source == "extract" {
			s.extractBuild = b
		}
	}
	if s.extractBuild.Usages != s.f.GoUsages() {
		t.Fatalf("the extractor's build holds %d usages, want %d", s.extractBuild.Usages, s.f.GoUsages())
	}
}

// captureJSON is `glossa capture --json`.
type captureJSON struct {
	Captures []struct {
		Route    string `json:"route"`
		Locale   string `json:"locale"`
		Viewport struct {
			Width, Height int
		} `json:"viewport"`
		Regions int `json:"regions"`
		Visible int `json:"visible"`
		Renders int `json:"renders"`
		Image   struct {
			SHA256        string `json:"sha256"`
			Width, Height int
		} `json:"image"`
	} `json:"captures"`
	Upload *struct {
		Build              string   `json:"build"`
		Captures           int      `json:"captures"`
		ImagesStored       int      `json:"images_stored"`
		ImagesDeduplicated int      `json:"images_deduplicated"`
		UnknownKeys        []string `json:"unknown_keys"`
	} `json:"upload"`
	Coverage struct {
		Messages    int `json:"messages"`
		Captured    int `json:"captured"`
		NotCaptured []struct {
			Key string `json:"key"`
		} `json:"not_captured"`
	} `json:"coverage"`
}

// captureRoutes screenshots the preview build of the app over its eight
// routes, in German and Japanese, at both viewports. The browser is the
// test's own, attached through GLOSSA_CAPTURE_CDP, because a CI runner's
// kernel denies Chrome its own sandbox (platform/README.md).
func (s *scenario) captureRoutes() {
	t := s.t
	app := serveApp(t, filepath.Join(appDir(), fixture.DirPreview))
	s.appURL = app.URL
	s.ci.env["GLOSSA_CAPTURE_CDP"] = s.chrome
	var out captureJSON
	s.ci.ok(&out, "capture", "--upload", "--base-url", app.URL, "--commit", s.f.Commit, "--branch", s.f.Branch)
	want := len(s.f.Pages) * len(s.f.CaptureLocales) * len(s.f.Viewports)
	if len(out.Captures) != want || out.Upload == nil || out.Upload.Captures != want {
		t.Fatalf("captured %d pages, want %d routes × %d locales × %d viewports = %d (upload %+v)",
			len(out.Captures), len(s.f.Pages), len(s.f.CaptureLocales), len(s.f.Viewports), want, out.Upload)
	}
	if len(out.Upload.UnknownKeys) > 0 {
		t.Errorf("the capture saw keys the catalog doesn't have: %v", out.Upload.UnknownKeys)
	}
	if len(out.Coverage.NotCaptured) > 0 {
		t.Errorf("%d of %d messages were never on screen: %s",
			len(out.Coverage.NotCaptured), out.Coverage.Messages, joinKeys(keysOf(out)))
	}
	s.capture = out
}

func keysOf(out captureJSON) []string {
	var keys []string
	for _, n := range out.Coverage.NotCaptured {
		keys = append(keys, n.Key)
	}
	return keys
}

// messageUsages is `GET …/messages/{key}/usages`.
type messageUsages struct {
	MessageID string `json:"message_id"`
	Key       string `json:"key"`
	Usages    []struct {
		File      string `json:"file"`
		Line      int    `json:"line"`
		Column    int    `json:"column"`
		Component string `json:"component"`
		Route     string `json:"route"`
		Kind      string `json:"kind"`
		Source    string `json:"source"`
		Commit    string `json:"commit"`
	} `json:"usages"`
}

// messageCaptures is `GET …/messages/{key}/captures`.
type messageCaptures struct {
	Key      string `json:"key"`
	Captures []struct {
		Route    string `json:"route"`
		Locale   string `json:"locale"`
		Viewport struct {
			Width, Height int
		} `json:"viewport"`
		Image struct {
			Digest        string `json:"digest"`
			Width, Height int
			URL           string `json:"url"`
		} `json:"image"`
		Regions []struct {
			Kind    string `json:"kind"`
			Visible bool   `json:"visible"`
			Box     struct {
				X, Y, Width, Height int
			} `json:"box"`
		} `json:"regions"`
	} `json:"captures"`
}

// coverage is the exit criterion of RFC 0004 §1: every active message
// has at least one usage that names a file, a line and a component, and
// at least one visible region.
type coverage struct {
	messages int
	// withUsage, withComponent and withRegion count the messages that
	// have one; the three lists name the ones that don't.
	noUsage, noComponent, noRegion []string
	usages, regions, visible       int
	// kinds and sources count the usages by kind and by collector.
	kinds, sources map[string]int
	// locales and routes count the visible regions by capture.
	locales, routes map[string]int
}

// checkCoverage reads back, for every active message, where the platform
// says it appears.
func (s *scenario) checkCoverage() {
	t := s.t
	var unused struct {
		Items []struct {
			Key string `json:"key"`
		} `json:"items"`
		CurrentBuilds  int `json:"current_builds"`
		ActiveMessages int `json:"active_messages"`
		UnusedMessages int `json:"unused_messages"`
	}
	s.owner.do(http.MethodGet, s.projectPath("/unused-messages?page_size=100"), nil, http.StatusOK, &unused)
	if unused.CurrentBuilds == 0 || unused.ActiveMessages != len(s.f.Messages) {
		t.Fatalf("unused-messages = %+v, want %d active messages over at least one build", unused, len(s.f.Messages))
	}

	c := &coverage{
		messages: len(s.f.Messages),
		kinds:    map[string]int{}, sources: map[string]int{},
		locales: map[string]int{}, routes: map[string]int{},
	}
	var mu safeCoverage
	errs := parallel(s.f.Keys(), 8, func(key string) error {
		var u messageUsages
		if _, err := s.owner.try(http.MethodGet, s.projectPath("/messages/"+key+"/usages"), nil, http.StatusOK, &u); err != nil {
			return fmt.Errorf("usages of %s: %w", key, err)
		}
		var cap messageCaptures
		if _, err := s.owner.try(http.MethodGet, s.projectPath("/messages/"+key+"/captures"), nil, http.StatusOK, &cap); err != nil {
			return fmt.Errorf("captures of %s: %w", key, err)
		}
		mu.add(c, key, u, cap)
		return nil
	})
	for _, err := range errs {
		t.Error(err)
	}
	sort.Strings(c.noUsage)
	sort.Strings(c.noComponent)
	sort.Strings(c.noRegion)
	if len(c.noUsage) > 0 {
		t.Errorf("%d of %d messages have no usage: %s", len(c.noUsage), c.messages, joinKeys(c.noUsage))
	}
	if len(c.noComponent) > 0 {
		t.Errorf("%d of %d messages have no usage naming a file, a line and a component: %s",
			len(c.noComponent), c.messages, joinKeys(c.noComponent))
	}
	if len(c.noRegion) > 0 {
		t.Errorf("%d of %d messages have no visible region: %s", len(c.noRegion), c.messages, joinKeys(c.noRegion))
	}
	s.coverage = c
}

// safeCoverage guards the counters the workers fill.
type safeCoverage struct{ mu sync.Mutex }

func (s *safeCoverage) add(c *coverage, key string, u messageUsages, cap messageCaptures) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(u.Usages) == 0 {
		c.noUsage = append(c.noUsage, key)
	}
	located := false
	for _, usage := range u.Usages {
		c.usages++
		c.kinds[usage.Kind]++
		c.sources[usage.Source]++
		if usage.File != "" && usage.Line > 0 && usage.Component != "" {
			located = true
		}
	}
	if !located {
		c.noComponent = append(c.noComponent, key)
	}
	visible := false
	for _, shot := range cap.Captures {
		for _, r := range shot.Regions {
			c.regions++
			if !r.Visible {
				continue
			}
			c.visible++
			visible = true
			c.locales[shot.Locale]++
			c.routes[shot.Route]++
		}
	}
	if !visible {
		c.noRegion = append(c.noRegion, key)
	}
}

// checkImages reads one capture's image back through the API, the way
// Studio's "Where it appears" pane does.
func (s *scenario) checkImages() {
	t := s.t
	key := s.f.Messages[0].Key
	var cap messageCaptures
	s.owner.do(http.MethodGet, s.projectPath("/messages/"+key+"/captures"), nil, http.StatusOK, &cap)
	if len(cap.Captures) == 0 {
		t.Fatalf("%s has no capture", key)
	}
	shot := cap.Captures[0]
	h, err := s.owner.send(http.MethodGet, shot.Image.URL, nil, "", http.StatusOK, nil)
	if err != nil {
		t.Fatalf("read the image of %s: %v", key, err)
	}
	if ct := h.Get("Content-Type"); ct != "image/png" {
		t.Errorf("image content type %q, want image/png", ct)
	}
	if etag := h.Get("ETag"); etag != `"`+shot.Image.Digest+`"` {
		t.Errorf("image ETag %s, want the digest %s", etag, shot.Image.Digest)
	}
	if !strings.Contains(h.Get("Cache-Control"), "private") {
		t.Errorf("image Cache-Control %q, want a private one", h.Get("Cache-Control"))
	}
	s.imageBytes = shot.Image.Width * shot.Image.Height
}

// waitForIngest waits until the context reads see a build, because the
// upload answers before its events have been handled.
func (s *scenario) waitForIngest(what string, want int) {
	eventually(s.t, 30*time.Second, what, func() (bool, string) {
		builds := list[contextBuild](s.owner, s.projectPath("/context-builds"), nil)
		return len(builds) >= want, fmt.Sprintf("%d builds", len(builds))
	})
}

func joinKeys(keys []string) string {
	sort.Strings(keys)
	if len(keys) > 8 {
		return strings.Join(keys[:8], ", ") + fmt.Sprintf(" … (%d)", len(keys))
	}
	return strings.Join(keys, ", ")
}
