//go:build system

package m5_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	yaml "go.yaml.in/yaml/v3"
)

// ── §12.2 Vendor visibility on every surface ─────────────────────────

// sweepRow is one operation of the generated sweep, called as the
// vendor member: its verdict, and why.
type sweepRow struct {
	Operation string
	Path      string
	Inside    string
	Outside   string
	OK        bool
	// Unexercised says the sweep had no id to address the operation
	// with. §12.2 counts it as a failure: an operation with no verdict
	// is one nobody proved.
	Unexercised string
	Why         string
}

// operation is one GET of platform/api/openapi.yaml.
type operation struct {
	ID        string
	Path      string
	Responses map[string]bool
	// Query are the operation's required query parameters. A request
	// without them is malformed and answered 400 before anything is
	// authorized — identically inside and outside the assignment, which
	// proves nothing — so the sweep fills them like path parameters.
	Query []string
}

// specParam is a parameter as the spec writes it: inline, or a $ref
// into components.parameters.
type specParam struct {
	Ref      string `yaml:"$ref"`
	Name     string `yaml:"name"`
	In       string `yaml:"in"`
	Required bool   `yaml:"required"`
}

// getOperations reads every GET operation from the contract. The sweep
// is generated from it, so an endpoint added without assignment-scoped
// visibility fails here without anyone remembering to add it (§11.1.4).
func getOperations() ([]operation, error) {
	raw, err := os.ReadFile(filepath.Join(platformDir(), "api", "openapi.yaml"))
	if err != nil {
		return nil, err
	}
	var doc struct {
		Paths      map[string]map[string]yaml.Node `yaml:"paths"`
		Components struct {
			Parameters map[string]specParam `yaml:"parameters"`
		} `yaml:"components"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	requiredQuery := func(ps []specParam) []string {
		var out []string
		for _, p := range ps {
			if p.Ref != "" {
				p = doc.Components.Parameters[p.Ref[strings.LastIndex(p.Ref, "/")+1:]]
			}
			if p.In == "query" && p.Required {
				out = append(out, p.Name)
			}
		}
		return out
	}
	var ops []operation
	for path, methods := range doc.Paths {
		node, ok := methods["get"]
		if !ok {
			continue
		}
		var op struct {
			OperationID string               `yaml:"operationId"`
			Responses   map[string]yaml.Node `yaml:"responses"`
			Parameters  []specParam          `yaml:"parameters"`
		}
		if err := node.Decode(&op); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		var shared []specParam
		if n, ok := methods["parameters"]; ok {
			if err := n.Decode(&shared); err != nil {
				return nil, fmt.Errorf("%s parameters: %w", path, err)
			}
		}
		codes := map[string]bool{}
		for c := range op.Responses {
			codes[c] = true
		}
		ops = append(ops, operation{ID: op.OperationID, Path: path, Responses: codes,
			Query: requiredQuery(append(shared, op.Parameters...))})
	}
	sort.Slice(ops, func(i, j int) bool { return ops[i].Path < ops[j].Path })
	return ops, nil
}

var pathParam = regexp.MustCompile(`\{([a-z_]+)\}`)

func (s *scenario) vendorVisibility() {
	const id = "12.2"
	s.step(id, "the vendor exists and its translator is a vendor member with visibility `assigned`, scoped to project B", func() error {
		switch {
		case s.vendorID == "":
			return fmt.Errorf("the platform could not create a vendor (POST %s); the member is an ordinary `de` translator", s.vendorsPath())
		case !s.vendorAsVendor:
			return fmt.Errorf("the invitation could not carry `visibility: %s`; the member is an ordinary `de` translator", visibilityAssigned)
		}
		return nil
	})
	var assignment string
	s.step(id, fmt.Sprintf("assign %d `de` units of project B to the vendor", assignedUnits), func() error {
		// One assignment is one project's batch: the project once, the
		// units by message key and locale.
		units := make([]map[string]string, 0, len(s.assigned))
		for _, k := range s.assigned {
			units = append(units, map[string]string{"message": k, "locale": "de"})
		}
		assignee := map[string]any{"vendor": s.vendorID}
		if s.vendorID == "" {
			assignee = map[string]any{"member": s.vendorInvite}
		}
		var a struct {
			ID string `json:"id"`
		}
		if _, err := s.owner.try(http.MethodPost, s.assignmentsPath(), map[string]any{
			"project_id": s.projectB, "units": units, "assignee": assignee, "due_at": time.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339),
		}, http.StatusCreated, &a); err != nil {
			return missing("creating an assignment (POST "+s.assignmentsPath()+")", err)
		}
		assignment = a.ID
		return nil
	})
	// The sweep runs whatever happened above: with no vendor and no
	// assignment it shows exactly what a member restricted by nothing
	// can read — every one of those reads is a leak §12.2 exists to
	// catch.
	s.resolveSweepIDs()
	if assignment != "" {
		s.inside["assignment"] = assignment
	}
	s.generatedSweep()
	s.writesOutside()
	s.refusedOperations()
	s.mcpReadTools()
}

// resolveSweepIDs fills, for every path parameter the contract uses, an
// id inside the assignment (project B, an assigned unit) and one
// outside it (project A, an unassigned unit), by reading the parent
// collection as the owner.
func (s *scenario) resolveSweepIDs() {
	s.inside = map[string]string{}
	s.outside = map[string]string{}
	s.inside["tenant"], s.outside["tenant"] = s.tenant, s.tenant
	s.inside["project"], s.outside["project"] = s.projectB, s.projectA
	s.inside["message"], s.outside["message"] = s.assigned[0], s.unassigned[0]
	s.inside["locale"], s.outside["locale"] = "de", "de"
	s.inside["environment"], s.outside["environment"] = "production", "production"
	for k, v := range s.sweepInside {
		s.inside[k] = v
	}
	for k, v := range s.sweepOutside {
		s.outside[k] = v
	}
}

// withQuery adds op's required query parameters from ids, or names the
// first one the fixture has no value for.
func withQuery(path string, op operation, ids map[string]string) (string, string) {
	q := url.Values{}
	for _, name := range op.Query {
		v, ok := ids[name]
		if !ok {
			return path, name
		}
		q.Set(name, v)
	}
	if len(q) == 0 {
		return path, ""
	}
	return path + "?" + q.Encode(), ""
}

// leakMarkers are strings only something outside the assignment holds:
// project A's id, its keys, its texts and canaries, and project B's
// unassigned keys and texts.
func (s *scenario) leakMarkers() []string {
	m := []string{s.projectA, canaries[0], canaries[2], "of the a screen"}
	for i := 1; i <= messagesPerProject; i++ {
		m = append(m, unitKey("a", i))
	}
	for _, k := range s.unassigned {
		m = append(m, k)
	}
	for i := assignedUnits + 1; i <= messagesPerProject; i++ {
		m = append(m, fmt.Sprintf("Entry %d of the b screen", i))
	}
	return m
}

func leaked(body []byte, markers []string) string {
	for _, m := range markers {
		if m != "" && bytes.Contains(body, []byte(m)) {
			return m
		}
	}
	return ""
}

// resolve fills a path template; ok is false when a parameter has no
// fixture id on that side.
func (s *scenario) resolve(path string, ids map[string]string, side string) (string, string, bool) {
	missingParam := ""
	out := pathParam.ReplaceAllStringFunc(path, func(m string) string {
		name := m[1 : len(m)-1]
		if v, ok := ids[name]; ok {
			return v
		}
		if v := s.lookupID(path, name, side); v != "" {
			ids[name] = v
			return v
		}
		if missingParam == "" {
			missingParam = name
		}
		return m
	})
	return out, missingParam, missingParam == ""
}

// lookupID finds an id for a parameter by listing its parent collection
// as the owner: for …/releases/{release}, GET …/releases on the side's
// project, and the first item's id (or name, key, code).
func (s *scenario) lookupID(template, name, side string) string {
	i := strings.Index(template, "{"+name+"}")
	if i <= 0 {
		return ""
	}
	parent := strings.TrimSuffix(template[:i], "/")
	ids := s.inside
	if side == "outside" {
		ids = s.outside
	}
	resolved, _, ok := s.resolve(parent, ids, side)
	if !ok {
		return ""
	}
	status, body, err := s.owner.get(resolved + "?page_size=5")
	if err != nil || status != http.StatusOK {
		return ""
	}
	// The parameter's own name first: {version} is a version's number,
	// and the first "id" in a list of versions is not one.
	for _, field := range []string{name, "id", "name", "key", "code", "digest", "number", "version"} {
		re := regexp.MustCompile(`"` + field + `"\s*:\s*"?([^",}]+)"?`)
		if m := re.FindSubmatch(body); m != nil {
			return string(m[1])
		}
	}
	return ""
}

// missingVerdicts are the operations of the spec with no row in the
// coverage table. It is the harness's own check that the table cannot
// silently shrink; a row that names why it was not exercised counts as
// a row, and fails the criterion separately.
func missingVerdicts(ops []operation, rows []sweepRow) []string {
	have := map[string]bool{}
	for _, r := range rows {
		have[r.Operation] = true
	}
	var missing []string
	for _, op := range ops {
		if !have[op.ID] {
			missing = append(missing, op.ID)
		}
	}
	return missing
}

func (s *scenario) generatedSweep() {
	const id = "12.2"
	ops, err := getOperations()
	if err != nil {
		s.gap(id, "the sweep could not read platform/api/openapi.yaml: %v", err)
		return
	}
	markers := s.leakMarkers()
	var leaks, unexercised, undocumented []string
	for _, op := range ops {
		row := sweepRow{Operation: op.ID, Path: op.Path, OK: true}
		inPath, param, ok := s.resolve(op.Path, cloneIDs(s.inside), "inside")
		if !ok {
			row.OK, row.Unexercised = false, "no fixture id for `{"+param+"}`"
			unexercised = append(unexercised, op.ID)
			s.sweep = append(s.sweep, row)
			continue
		}
		if inPath, param = withQuery(inPath, op, s.inside); param != "" {
			row.OK, row.Unexercised = false, "no fixture value for the required query parameter `"+param+"`"
			unexercised = append(unexercised, op.ID)
			s.sweep = append(s.sweep, row)
			continue
		}
		status, body, err := s.vendor.get(inPath)
		switch {
		case err != nil:
			row.OK, row.Inside, row.Why = false, "error", err.Error()
		case !op.Responses[fmt.Sprint(status)]:
			row.OK, row.Inside, row.Why = false, fmt.Sprint(status), fmt.Sprintf("%d is not a documented response", status)
			undocumented = append(undocumented, op.ID)
		default:
			row.Inside = fmt.Sprint(status)
			if m := leaked(body, markers); m != "" && status == http.StatusOK {
				row.OK, row.Why = false, "the answer holds `"+m+"`, which is outside the assignment"
				leaks = append(leaks, op.ID)
			}
		}
		// The outside calls, where the operation is addressed by
		// something the assignment can be outside of: another project
		// (A), and — for a message — a unit of the assignment's own
		// project that is not in it.
		var outs []string
		for _, side := range s.outsideSides(op.Path) {
			outPath, _, ok := s.resolve(op.Path, side, "outside")
			if !ok {
				continue
			}
			if outPath, param = withQuery(outPath, op, side); param != "" || outPath == inPath {
				continue
			}
			st, ob, err := s.vendor.get(outPath)
			outs = append(outs, fmt.Sprint(st))
			if err != nil {
				// A call that did not complete proves nothing about the
				// outside; it must not read as a pass.
				if row.OK {
					row.Why = "the outside call failed: " + err.Error()
					undocumented = append(undocumented, op.ID)
				}
				row.OK = false
				continue
			}
			if st != http.StatusNotFound && st != http.StatusUnauthorized {
				if row.OK {
					row.Why = fmt.Sprintf("an id outside the assignment answered %d, want 404", st)
					if m := leaked(ob, markers); m != "" {
						row.Why += " (it holds `" + m + "`)"
					}
					leaks = append(leaks, op.ID)
				}
				row.OK = false
			}
		}
		row.Outside = strings.Join(outs, ", ")
		s.sweep = append(s.sweep, row)
	}
	s.note(id, "The sweep generated %d GET operations from platform/api/openapi.yaml.", len(ops))
	if missing := missingVerdicts(ops, s.sweep); len(missing) > 0 {
		s.gap(id, "the coverage table has no row for %d operations of the spec (%s); an operation with no verdict is a failure",
			len(missing), strings.Join(first(missing, 8), ", "))
	}
	if len(leaks) > 0 {
		s.gap(id, "the generated sweep: %d of %d GET operations show the vendor member something outside the assignment "+
			"(first: %s) — reads are not filtered through `Assignments.Covers`", len(leaks), len(ops), strings.Join(first(leaks, 5), ", "))
	}
	if len(undocumented) > 0 {
		s.gap(id, "the generated sweep: %d operations answered the vendor member with an undocumented status (first: %s)",
			len(undocumented), strings.Join(first(undocumented, 5), ", "))
	}
	if len(unexercised) > 0 {
		s.gap(id, "the generated sweep: %d operations have no verdict because the fixture had no id to address them (%s); "+
			"an operation with no verdict is a failure", len(unexercised), strings.Join(first(unexercised, 8), ", "))
	}
}

// outsideSides are the id sets an operation is called with outside the
// assignment: project A for anything addressed by a project, or by a
// tenant-level id the fixture made in project A (an import job, an AI
// fill, a Git connection); and project B with an unassigned unit for
// anything addressed by a message.
func (s *scenario) outsideSides(path string) []map[string]string {
	var sides []map[string]string
	if strings.Contains(path, "{project}") || s.outsideByID(path) {
		a := cloneIDs(s.outside)
		a["message"] = unitKey("a", 1)
		sides = append(sides, a)
	}
	if strings.Contains(path, "{message}") {
		b := cloneIDs(s.inside)
		b["message"] = s.unassigned[0]
		sides = append(sides, b)
	}
	return sides
}

// outsideByID says path is addressed by an id that has a value of its
// own outside the assignment: one the fixture made in project A.
func (s *scenario) outsideByID(path string) bool {
	for _, m := range pathParam.FindAllStringSubmatch(path, -1) {
		if out, ok := s.outside[m[1]]; ok && m[1] != "tenant" && out != s.inside[m[1]] {
			return true
		}
	}
	return false
}

func cloneIDs(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func first(xs []string, n int) []string {
	if len(xs) > n {
		return append(xs[:n:n], fmt.Sprintf("… %d more", len(xs)-n))
	}
	return xs
}

// writesOutside: the vendor member writes a unit outside the
// assignment in project B, and one in project A. Both must be refused.
func (s *scenario) writesOutside() {
	s.step("12.2", "writes outside the assignment are refused", func() error {
		var wrote []string
		for _, target := range []struct{ project, key string }{
			{s.projectB, s.unassigned[0]}, {s.projectA, unitKey("a", 1)},
		} {
			err := s.vendor.putTranslation(s.projectPathOf(target.project, "/messages/"+target.key+"/translations/de"),
				map[string]any{"text": "Vom Anbieter überschrieben", "syntax": "mf2", "origin": "human"})
			if err == nil {
				wrote = append(wrote, "`"+target.key+"`")
			}
		}
		if len(wrote) > 0 {
			return fmt.Errorf("the vendor member wrote %s, outside the assignment", strings.Join(wrote, " and "))
		}
		return nil
	})
}

// refusedOperations: export, import and TM search (§3.3).
func (s *scenario) refusedOperations() {
	s.step("12.2", "export, import and TM search are refused", func() error {
		var allowed []string
		// The bodies are valid, so anything but a refusal is the
		// operation being allowed: a 2xx, or a 4xx about something
		// other than permission.
		for _, job := range []struct{ what, path string }{
			{"an export job", "/export-jobs"}, {"an import job", "/import-jobs"},
		} {
			_, err := s.vendor.try(http.MethodPost, s.tenantPath(job.path),
				map[string]any{"project_id": s.projectB, "format": "xliff"}, http.StatusAccepted, nil)
			if st := statusOf(err); err == nil || st != http.StatusForbidden && st != http.StatusNotFound {
				allowed = append(allowed, fmt.Sprintf("%s (%s)", job.what, statusText(err)))
			}
		}
		for _, p := range []string{"/tm-lookups?locale=de&source_locale=en&text=Entry", "/tm-concordance?locale=de&source_locale=en&q=Entry"} {
			if st, _, err := s.vendor.get(s.tenantPath(p)); err == nil && st != http.StatusForbidden && st != http.StatusNotFound {
				allowed = append(allowed, fmt.Sprintf("`GET %s` (%d)", strings.SplitN(p, "?", 2)[0], st))
			}
		}
		if len(allowed) > 0 {
			return fmt.Errorf("the vendor member was not refused: %s", strings.Join(allowed, ", "))
		}
		return nil
	})
}

func statusText(err error) string {
	if err == nil {
		return "accepted"
	}
	if st := statusOf(err); st != 0 {
		return fmt.Sprint(st)
	}
	return err.Error()
}

// mcpReadTools: the MCP read tools, as the vendor member, through a
// token of theirs. A member who cannot hold a token cannot reach MCP,
// which is also a pass for this part.
func (s *scenario) mcpReadTools() {
	const id = "12.2"
	var token struct {
		Secret string `json:"secret"`
	}
	if _, err := s.vendor.try(http.MethodPost, s.tenantPath("/tokens"),
		map[string]any{"name": "vera-mcp", "scopes": []string{"read"}}, http.StatusCreated, &token); err != nil {
		s.note(id, "The vendor member could not create an API token (%v), so no MCP tool is reachable as them. "+
			"Each tool is listed from an owner's session with the verdict \"unreachable as the member\"; none was called.", err)
		s.mcpSweep = append(s.mcpSweep, sweepRow{Operation: "(connect)", OK: true, Inside: "no token", Why: "refused at token creation"})
		s.listMCPToolsUnreachable()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sess, err := s.mcpSession(ctx, token.Secret)
	if err != nil {
		s.note(id, "The vendor member's token could not open an MCP session (%v).", err)
		s.mcpSweep = append(s.mcpSweep, sweepRow{Operation: "(connect)", OK: true, Inside: "refused", Why: err.Error()})
		s.listMCPToolsUnreachable()
		return
	}
	defer sess.Close()
	tools, err := sess.ListTools(ctx, nil)
	if err != nil {
		s.gap(id, "the vendor member's MCP session could not list its tools: %v", err)
		return
	}
	markers := s.leakMarkers()
	var leaks, unexercised []string
	for _, tool := range tools.Tools {
		args := map[string]any{}
		var required []any
		if schema, ok := tool.InputSchema.(map[string]any); ok {
			props, _ := schema["properties"].(map[string]any)
			required, _ = schema["required"].([]any)
			for name := range props {
				switch name {
				case "project", "project_id":
					args[name] = s.projectA
				case "message", "key", "message_key":
					args[name] = unitKey("a", 1)
				case "locale":
					args[name] = "de"
				case "query", "q", "text":
					args[name] = "Entry"
				}
			}
		}
		row := sweepRow{Operation: tool.Name, Path: "mcp", OK: true}
		if name := unfilledRequired(required, args); name != "" {
			// A call missing a required argument is refused as malformed,
			// whoever makes it: that is no verdict about visibility.
			row.OK, row.Unexercised = false, "no fixture value for the required argument `"+name+"`"
			unexercised = append(unexercised, tool.Name)
			s.mcpSweep = append(s.mcpSweep, row)
			continue
		}
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: tool.Name, Arguments: args})
		switch {
		case err != nil:
			row.Inside = "error: " + err.Error()
		case res.IsError:
			row.Inside = "refused"
		default:
			var b strings.Builder
			for _, c := range res.Content {
				if t, ok := c.(*mcp.TextContent); ok {
					b.WriteString(t.Text)
				}
			}
			row.Inside = "answered"
			if m := leaked([]byte(b.String()), markers); m != "" {
				row.OK, row.Why = false, "the answer holds `"+m+"`, which is outside the assignment"
				leaks = append(leaks, tool.Name)
			}
		}
		s.mcpSweep = append(s.mcpSweep, row)
	}
	if len(leaks) > 0 {
		s.gap(id, "MCP: %d of %d tools answered the vendor member with something outside the assignment (first: %s)",
			len(leaks), len(tools.Tools), strings.Join(first(leaks, 5), ", "))
	}
	if len(unexercised) > 0 {
		s.gap(id, "MCP: %d tools have no verdict because the fixture had no value for a required argument (%s)",
			len(unexercised), strings.Join(first(unexercised, 8), ", "))
	}
}

// unfilledRequired names the first required argument args lacks.
func unfilledRequired(required []any, args map[string]any) string {
	for _, r := range required {
		if name, ok := r.(string); ok {
			if _, have := args[name]; !have {
				return name
			}
		}
	}
	return ""
}

func (s *scenario) mcpSession(ctx context.Context, secret string) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "glossa-m5-exit", Version: "0"}, nil)
	return client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:   s.d.base + "/mcp",
		HTTPClient: &http.Client{Transport: bearerRT{token: secret, next: http.DefaultTransport}, Timeout: 60 * time.Second},
	}, nil)
}

// listMCPToolsUnreachable puts every MCP tool into the coverage table
// when the member cannot reach MCP at all, listed through an owner's
// token: the table then shows what exists and that none of it was
// callable as the member, instead of one "(connect)" row for all of it.
func (s *scenario) listMCPToolsUnreachable() {
	const id = "12.2"
	var token struct {
		Secret string `json:"secret"`
	}
	if _, err := s.owner.try(http.MethodPost, s.tenantPath("/tokens"),
		map[string]any{"name": "owner-mcp-list", "scopes": []string{"read"}}, http.StatusCreated, &token); err != nil {
		s.note(id, "The MCP tools could not be listed for the table: the owner's token was refused (%v).", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	sess, err := s.mcpSession(ctx, token.Secret)
	if err != nil {
		s.note(id, "The MCP tools could not be listed for the table: %v", err)
		return
	}
	defer sess.Close()
	tools, err := sess.ListTools(ctx, nil)
	if err != nil {
		s.note(id, "The MCP tools could not be listed for the table: %v", err)
		return
	}
	for _, tool := range tools.Tools {
		s.mcpSweep = append(s.mcpSweep, sweepRow{Operation: tool.Name, Path: "mcp", OK: true,
			Inside: "unreachable", Why: "the member can hold no token, so cannot open a session; not called"})
	}
}

type bearerRT struct {
	token string
	next  http.RoundTripper
}

func (b bearerRT) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.next.RoundTrip(r)
}
