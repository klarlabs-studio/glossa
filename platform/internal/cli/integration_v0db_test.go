//go:build integration

package cli

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"go.klarlabs.de/glossa/platform/internal/cli/v0"
	"go.klarlabs.de/glossa/platform/internal/cli/v0/v0test"
)

// `glossa import --from v0 --v0-db` end to end on the CLI's side: a v0.3
// database from apps/api's migrations, backed up and restored with
// v0-restore.sh, imported into the fake platform.
func TestImportFromRestoredV03Database(t *testing.T) {
	ctx := context.Background()
	old, err := v0test.Start(ctx, v0test.Seed)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(old.Close)
	backup, err := old.Backup(ctx, "glossa-20261001")
	if err != nil {
		t.Fatal(err)
	}
	dsn, err := old.Restore(ctx, backup, "v0_restore")
	if err != nil {
		t.Fatal(err)
	}

	srv := newFakeServer(t)
	srv.sourceLocale, srv.locales = "de", []string{"de"}
	w := newWorkspace(t).withProject(srv, nil)
	w.write("glossa.yaml", strings.Replace(w.read("glossa.yaml"), "source_locale: en", "source_locale: de", 1))
	args := []string{"import", "--from", "v0", "--v0-db", dsn, "--v0-tenant", "klarlabs", "--v0-project", "brotwerk"}

	var doc errorDoc
	w.json(&doc, "import", "--from", "v0", "--v0-db", old.DSN(v0test.LiveDB), "--v0-tenant", "klarlabs", "--v0-project", "brotwerk").
		want(t, ExitUsage)
	if doc.Error.Code != "v0_not_a_restore" || !strings.Contains(doc.Error.Fix, "v0-restore.sh") || srv.countRequests("POST") != 0 {
		t.Fatalf("v0.3's own database: error = %+v", doc.Error)
	}

	var dry importJSON
	w.json(&dry, append(args, "--dry-run")...).want(t, ExitOK)
	if dry.Summary["message"]["planned"] != 3 || dry.Summary["translation"]["planned"] != 3 || srv.countRequests("POST") != 0 {
		t.Fatalf("dry run = %+v", dry.Summary)
	}

	var out importJSON
	w.json(&out, args...).want(t, ExitOK)
	if out.Summary["message"]["created"] != 3 || out.Summary["translation"]["created"] != 3 || strings.Join(out.LocalesAdded, ",") != "en,fr" {
		t.Fatalf("import = %+v / %v", out.Summary, out.LocalesAdded)
	}
	if srv.messages["cart.items"].description != v0test.CartDesc || srv.messages["checkout.pay"].description != v0test.CheckoutDesc ||
		srv.messages["home.title"].description != "" {
		t.Errorf("descriptions: cart %q, checkout %q, home %q", srv.messages["cart.items"].description,
			srv.messages["checkout.pay"].description, srv.messages["home.title"].description)
	}
	cart := srv.translations["en"]["cart.items"]
	d := cart.originDetail
	if cart.origin != "import" || d["source"] != "glossa-v0.3" || d["tenant"] != "klarlabs" || d["restore"] != "glossa-20261001.sql.gz" ||
		d["v0_status"] != "approved" || d["v0_updated_by"] != "v0:"+v0test.BobID || d["v0_updated_at"] != "2025-02-02T10:30:00Z" ||
		d["v0_translation_id"] != v0test.TrEnCart {
		t.Errorf("en cart.items provenance = %s %v", cart.origin, d)
	}
	if d := srv.translations["en"]["home.title"].originDetail; d["v0_updated_by"] != "v0:ai:openai" || d["v0_status"] != "ai_translated" {
		t.Errorf("en home.title provenance = %v", d)
	}
	if d, ok := srv.translations["fr"]["home.title"].originDetail["v0_updated_by"]; !ok || d != nil {
		t.Errorf("fr home.title: v0_updated_by = %v (present %v); unknown must be an explicit null", d, ok)
	}

	if out.Restore == nil || out.Restore.Database != "v0_restore" || out.Source["db"] == "" || strings.Contains(out.Source["db"], "postgres:postgres") {
		t.Errorf("restore = %+v, source = %v", out.Restore, out.Source)
	}
	labels := map[string]string{}
	for _, l := range out.Locales {
		labels[l.Code] = l.Label
	}
	if labels["de"] != "Deutsch" || labels["en"] != "English (UK)" || labels["fr"] != "Français" {
		t.Errorf("locales = %+v", out.Locales)
	}
	status := map[string]string{}
	for _, i := range out.Invitations {
		status[i.Email] = i.Status + " " + strings.Join(i.Roles, ",") + " " + strings.Join(i.Locales, ",")
	}
	if status["alice@example.com"] != "planned admin " || status["bob@example.com"] != "planned translator en" ||
		!strings.HasPrefix(status["carol@example.com"], "held translator") || len(status) != 3 {
		t.Errorf("invitations = %v", status)
	}
	actors := map[string]int{}
	for _, e := range out.AuditEntries {
		actors[e.Actor]++
	}
	if len(out.AuditEntries) != 4 || actors["v0:"+v0test.BobID] != 2 || actors["v0:ai:openai"] != 1 || actors["v0:unknown"] != 1 {
		t.Errorf("audit entries = %+v", out.AuditEntries)
	}
	if len(out.NotCarried) == 0 || !containsWarning(out.Warnings, "fr") {
		t.Errorf("not carried = %+v, warnings = %v", out.NotCarried, out.Warnings)
	}

	human := w.run(append(args, "--dry-run")...)
	for _, want := range []string{"restore v0_restore of glossa-20261001.sql.gz", `en "English (UK)"`, `fr "Français" (disabled)`,
		"2 invitations planned, 1 held", "carol@example.com", "4 history entries planned", "1 without a translation", "users.password_hash"} {
		if !strings.Contains(human.stdout, want) {
			t.Errorf("human output lacks %q:\n%s", want, human.stdout)
		}
	}
	if strings.Contains(human.stdout+human.stderr, "postgres:postgres") {
		t.Error("human output leaks the DSN's credentials")
	}

	// A re-run changes nothing.
	w.json(&out, args...).want(t, ExitOK)
	if out.Summary["message"]["unchanged"] != 3 || out.Summary["translation"]["unchanged"] != 3 {
		t.Errorf("re-run = %+v", out.Summary)
	}
	if srv.members.posts != 0 {
		t.Fatalf("without --invite the import sent %d invitations", srv.members.posts)
	}

	// --invite sends the planned invitations, never the held one, and
	// invites nobody twice (RFC 0006 §7.2).
	invite := append(slices.Clone(args), "--invite")
	srv.members.refuse = true
	w.json(&doc, invite...).want(t, ExitNetwork)
	if doc.Error.Code != "forbidden" || !strings.Contains(doc.Error.Fix, "admin scope") {
		t.Errorf("a token that may not invite: error = %+v", doc.Error)
	}
	srv.members.refuse = false
	srv.member("BOB@example.com", "translator")
	srv.members.posts = 0
	w.json(&out, invite...).want(t, ExitOK)
	sent := map[string]string{}
	for _, i := range out.Invitations {
		sent[i.Email] = i.Status
		if i.Status != v0.InvitationHeld && i.MemberID == "" {
			t.Errorf("%s is %s without a member id", i.Email, i.Status)
		}
	}
	if sent["alice@example.com"] != "invited" || sent["bob@example.com"] != "exists" || sent["carol@example.com"] != "held" ||
		srv.members.posts != 1 {
		t.Fatalf("invitations = %v after %d invitations sent", sent, srv.members.posts)
	}
	alice := srv.members.items[len(srv.members.items)-1]
	if alice["email"] != "alice@example.com" || fmt.Sprint(alice["roles"]) != "[admin]" {
		t.Errorf("alice was invited as %v", alice)
	}
	human = w.run(invite...)
	if !strings.Contains(human.stdout, "0 invitations sent, 2 already members or invited, 0 failed, 1 held") {
		t.Errorf("a second --invite run reads:\n%s", human.stdout)
	}
	if srv.members.posts != 1 {
		t.Errorf("a second --invite run sent %d more invitations", srv.members.posts-1)
	}
	if srv.audit.posts != 0 {
		t.Fatalf("without --history the import sent %d history imports", srv.audit.posts)
	}

	// --history sends the audit-entry plan as digests (RFC 0006 §7.2).
	history := append(slices.Clone(args), "--history")
	srv.audit.refuse = true
	w.json(&doc, history...).want(t, ExitNetwork)
	if doc.Error.Code != "forbidden" || !strings.Contains(doc.Error.Fix, "only an owner") {
		t.Errorf("a credential that is not an owner's: error = %+v", doc.Error)
	}
	srv.audit.refuse = false
	w.json(&out, history...).want(t, ExitOK)
	if out.History == nil || out.History.Sent != 4 || out.History.Recorded != 4 || out.History.Existing != 0 {
		t.Fatalf("history = %+v", out.History)
	}
	gone := srv.audit.recorded[strconv.FormatInt(out.AuditEntries[0].V0ID, 10)]
	for _, e := range out.AuditEntries {
		if e.Unresolved != "" {
			gone = srv.audit.recorded[strconv.FormatInt(e.V0ID, 10)]
		}
	}
	if gone["unresolved"] != "translation_deleted" || gone["key"] != nil || gone["before_sha256"] == nil || gone["actor"] != "v0:unknown" {
		t.Errorf("the row whose translation is gone was sent as %v", gone)
	}
	// Every v0.3 value the plan holds is absent from every request; its
	// digest is there instead.
	for _, e := range out.AuditEntries {
		for _, text := range []*string{e.Before, e.After} {
			if text == nil {
				continue
			}
			for _, body := range srv.audit.bodies {
				if strings.Contains(string(body), *text) {
					t.Errorf("v0.3's text %q was sent to the audit import", *text)
				}
			}
			if !strings.Contains(string(srv.audit.bodies[len(srv.audit.bodies)-1]), textSHA256(text)) {
				t.Errorf("the digest of %q was not sent", *text)
			}
		}
	}
	// Again — or for another project of the tenant, whose plan carries
	// the row whose translation is gone too — records nothing twice.
	w.json(&out, history...).want(t, ExitOK)
	if out.History.Recorded != 0 || out.History.Existing != 4 || len(srv.audit.recorded) != 4 {
		t.Errorf("a second --history run = %+v, %d rows held", out.History, len(srv.audit.recorded))
	}
	human = w.run(history...)
	if !strings.Contains(human.stdout, "4 history entries sent to the audit trail (1 without a translation): 0 recorded, 4 already there") {
		t.Errorf("--history reads:\n%s", human.stdout)
	}
}

func containsWarning(ws []string, sub string) bool {
	for _, w := range ws {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}
