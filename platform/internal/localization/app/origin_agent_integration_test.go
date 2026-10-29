//go:build integration

package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"testing"

	"github.com/jackc/pgx/v5"

	"go.klarlabs.de/glossa/platform/db/migrations"
	"go.klarlabs.de/glossa/platform/internal/localization/app"
	"go.klarlabs.de/glossa/platform/internal/localization/domain"
)

// `agent` against a real Postgres (migration 0032): that the value
// reaches the column and comes back, that a query can pick agent-written
// translations out from AI-written ones — which is the whole point of
// giving the act its own origin — and that the migration widening the
// constraint rewrites nothing.

const mcpDetail = `{"via":"mcp","tool":"translation_propose"}`

// TestAnAgentWriteSurvivesTheRoundTrip: the origin an MCP write records
// is the origin the database stores, the one a later read returns and
// the one the revision log keeps, with origin_detail still saying which
// surface and tool wrote it.
func TestAnAgentWriteSurvivesTheRoundTrip(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, false, []string{"de"}, map[string]string{"home.title": "Welcome"})
	ctx := h.developer()

	state := string(domain.StateNeedsReview)
	written, _, err := h.svc.PutTranslation(ctx, p, "home.title", "de", app.TranslationInput{
		Text: "Willkommen", State: &state,
		Origin: string(domain.OriginAgent), OriginDetail: json.RawMessage(mcpDetail),
	}, nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if written.Origin != domain.OriginAgent {
		t.Fatalf("written origin = %q, want agent", written.Origin)
	}

	// Read back out of the database, not out of the write's own answer.
	got, err := h.svc.GetTranslation(ctx, p, "home.title", "de")
	if err != nil {
		t.Fatal(err)
	}
	if got.Origin != domain.OriginAgent {
		t.Errorf("read back origin = %q, want agent", got.Origin)
	}
	revs, _, err := h.svc.TranslationRevisions(ctx, p, "home.title", "de", firstPage())
	if err != nil || len(revs) != 1 {
		t.Fatalf("revisions: %v, %d of them", err, len(revs))
	}
	if revs[0].Provenance.Origin != domain.OriginAgent {
		t.Errorf("the revision log says %q, want agent", revs[0].Provenance.Origin)
	}
	// Postgres normalizes jsonb's spelling, so compare what it means.
	var detail map[string]string
	if err := json.Unmarshal(revs[0].Provenance.Detail, &detail); err != nil {
		t.Fatalf("origin_detail %s: %v", revs[0].Provenance.Detail, err)
	}
	if detail["via"] != "mcp" || detail["tool"] != "translation_propose" {
		t.Errorf("origin_detail = %v: it still says which surface and tool", detail)
	}
	if n := count(t, "SELECT count(*) FROM localization_translations WHERE origin = 'agent'"); n != 1 {
		t.Errorf("translations stored with origin agent = %d, want 1", n)
	}
	if n := count(t, "SELECT count(*) FROM localization_translation_revisions WHERE origin = 'agent'"); n != 1 {
		t.Errorf("revisions stored with origin agent = %d, want 1", n)
	}
}

// TestOriginSelectsAgentWritesApartFromAIWrites is the point of the
// change: before it, both acts were `ai` and told apart only by a JSON
// object no query could filter on. Now `WHERE origin = 'agent'` answers
// "what did the agents write", and `WHERE origin = 'ai'` no longer
// includes them.
func TestOriginSelectsAgentWritesApartFromAIWrites(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, false, []string{"de"}, map[string]string{
		"home.title":   "Welcome",
		"checkout.pay": "Pay now",
		"cart.items":   "Your cart",
	})
	ctx := h.developer()

	write := func(key, text string, origin domain.Origin, detail string) {
		t.Helper()
		in := app.TranslationInput{Text: text, Origin: string(origin)}
		if detail != "" {
			in.OriginDetail = json.RawMessage(detail)
		}
		if _, _, err := h.svc.PutTranslation(ctx, p, key, "de", in, nil); err != nil {
			t.Fatalf("write %s: %v", key, err)
		}
	}
	write("home.title", "Willkommen", domain.OriginAgent, mcpDetail)
	write("checkout.pay", "Jetzt zahlen", domain.OriginAI, `{"model":"a-model"}`)
	write("cart.items", "Dein Warenkorb", domain.OriginHuman, "")

	const byOrigin = `
		SELECT m.key FROM localization_translations t
		JOIN localization_messages m ON m.message_id = t.message_id
		WHERE t.origin = $1 ORDER BY m.key`
	keys := func(origin string) []string {
		t.Helper()
		rows, err := env.Super.Query(context.Background(), byOrigin, origin)
		if err != nil {
			t.Fatalf("query %s: %v", origin, err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var k string
			if err := rows.Scan(&k); err != nil {
				t.Fatal(err)
			}
			out = append(out, k)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := keys("agent"); len(got) != 1 || got[0] != "home.title" {
		t.Errorf("origin = agent selected %v, want [home.title]", got)
	}
	if got := keys("ai"); len(got) != 1 || got[0] != "checkout.pay" {
		t.Errorf("origin = ai selected %v, want [checkout.pay] and not the agent's", got)
	}
	if got := keys("human"); len(got) != 1 || got[0] != "cart.items" {
		t.Errorf("origin = human selected %v", got)
	}
	// The revision log answers the same question, which is where the
	// provenance actually lives.
	if n := count(t,
		"SELECT count(*) FROM localization_translation_revisions WHERE origin = 'agent'"); n != 1 {
		t.Errorf("agent revisions = %d, want 1", n)
	}
}

// TestTheListingFiltersByOrigin is the same question through the API
// rather than through SQL. AGENTS.md: anything that matters is
// reachable through the API, not only through the admin UI — and until
// wave 5 the difference between what an agent wrote and what the
// platform wrote on a person's behalf was answerable only by somebody
// with a psql prompt.
func TestTheListingFiltersByOrigin(t *testing.T) {
	h := newHarness(t)
	p := h.setup(t, false, []string{"de"}, map[string]string{
		"home.title":   "Welcome",
		"checkout.pay": "Pay now",
		"cart.items":   "Your cart",
	})
	ctx := h.developer()
	write := func(key, text string, origin domain.Origin, detail string) {
		t.Helper()
		in := app.TranslationInput{Text: text, Origin: string(origin)}
		if detail != "" {
			in.OriginDetail = json.RawMessage(detail)
		}
		if _, _, err := h.svc.PutTranslation(ctx, p, key, "de", in, nil); err != nil {
			t.Fatalf("write %s: %v", key, err)
		}
	}
	write("home.title", "Willkommen", domain.OriginAgent, mcpDetail)
	write("checkout.pay", "Jetzt zahlen", domain.OriginAI, `{"model":"a-model"}`)
	write("cart.items", "Dein Warenkorb", domain.OriginHuman, "")

	list := func(origins ...string) []string {
		t.Helper()
		return listAll(t, h, ctx, p, app.TranslationFilter{Locales: []string{"de"}, Origins: origins}, 50)
	}
	// The one that matters: `agent` and `ai` are different answers, and
	// neither contains the other.
	if got := list("agent"); len(got) != 1 || got[0] != "home.title/de" {
		t.Errorf("origin=agent listed %v, want [home.title/de]", got)
	}
	if got := list("ai"); len(got) != 1 || got[0] != "checkout.pay/de" {
		t.Errorf("origin=ai listed %v, want [checkout.pay/de] and not the agent's", got)
	}
	// Repeating the filter unions, like every other repeatable one.
	if got := list("agent", "ai"); len(got) != 2 {
		t.Errorf("origin=agent&origin=ai listed %v, want both", got)
	}
	// No filter is not "human": it is everything.
	if got := list(); len(got) != 3 {
		t.Errorf("no origin filter listed %v, want all three", got)
	}
	// A typo is a 400 and not a silent empty page, so nobody reads
	// "nothing an agent wrote" off a misspelt filter.
	_, _, err := h.svc.ListProjectTranslations(ctx, p,
		app.TranslationFilter{Locales: []string{"de"}, Origins: []string{"agents"}}, firstPage())
	if !errors.Is(err, domain.ErrInvalidOrigin) {
		t.Errorf("a misspelt origin answered %v, want ErrInvalidOrigin", err)
	}
}

// TestMigration0032WidensTheConstraintAndRewritesNothing: 0032 admits
// `agent` and leaves every existing row exactly as it was. Translations
// written through MCP before it stay `ai`, because that is what was true
// when they were written; they are still recognizable by their
// origin_detail, and the log is not edited to match a later vocabulary.
func TestMigration0032WidensTheConstraintAndRewritesNothing(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	p := h.setup(t, false, []string{"de"}, map[string]string{"home.title": "Welcome"})

	// A translation exactly as M4 wave 3 wrote them: `ai`, with the MCP
	// detail beside it.
	if _, _, err := h.svc.PutTranslation(h.developer(), p, "home.title", "de", app.TranslationInput{
		Text: "Willkommen", Origin: string(domain.OriginAI), OriginDetail: json.RawMessage(mcpDetail),
	}, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}
	before := originRows(t)
	if len(before) != 2 {
		t.Fatalf("seeded rows = %v, want the translation and its revision", before)
	}

	owner, err := pgx.Connect(ctx, env.OwnerDSN)
	if err != nil {
		t.Fatalf("owner connection: %v", err)
	}
	defer func() { _ = owner.Close(ctx) }()
	run := func(file string) {
		t.Helper()
		sql, err := fs.ReadFile(migrations.FS, file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if _, err := owner.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", file, err)
		}
	}

	// Back to 0004's six origins, then forward again over the same rows.
	run("0032_localization_origin_agent.down.sql")
	if agentAccepted(t) {
		t.Error("`agent` is accepted without 0032; the migration is not what admits it")
	}
	if got := originRows(t); !sameRows(got, before) {
		t.Errorf("reverting rewrote rows:\n before %v\n  after %v", before, got)
	}

	run("0032_localization_origin_agent.up.sql")
	if !agentAccepted(t) {
		t.Error("0032 did not widen the constraint to accept `agent`")
	}
	if got := originRows(t); !sameRows(got, before) {
		t.Errorf("the migration rewrote rows:\n before %v\n  after %v", before, got)
	}
	// Named for the avoidance of doubt: the seeded write stayed `ai`.
	if n := count(t, "SELECT count(*) FROM localization_translations WHERE origin = 'ai'"); n != 1 {
		t.Errorf("ai translations after 0032 = %d, want the one that was there", n)
	}
	if n := count(t, "SELECT count(*) FROM localization_translations WHERE origin = 'agent'"); n != 0 {
		t.Errorf("0032 turned %d row(s) into agent writes; it must convert none", n)
	}
}

// originRows is every stored origin with the row's identity and the
// timestamp a rewrite would disturb, translations first then revisions.
func originRows(t *testing.T) []string {
	t.Helper()
	const q = `
		SELECT id::text, origin, updated_at::text FROM localization_translations
		UNION ALL
		SELECT translation_id::text || '#' || revision, origin, origin_detail::text
		FROM localization_translation_revisions
		ORDER BY 1, 2, 3`
	rows, err := env.Super.Query(context.Background(), q)
	if err != nil {
		t.Fatalf("origin rows: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, origin, extra string
		if err := rows.Scan(&id, &origin, &extra); err != nil {
			t.Fatal(err)
		}
		out = append(out, id+" "+origin+" "+extra)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func sameRows(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// agentAccepted reports whether the schema currently allows `agent` in
// localization_translations.origin. It asks the constraint directly and
// rolls the answer back, so the question costs no state.
func agentAccepted(t *testing.T) bool {
	t.Helper()
	ctx := context.Background()
	tx, err := env.Super.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, "UPDATE localization_translations SET origin = 'agent'")
	return err == nil
}
