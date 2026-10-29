/**
 * M5's work surfaces end to end (RFC 0006 §3.1–§3.3), against the real
 * server: three people in one organisation, each in their own browser.
 *
 * - The **owner** makes the project, a vendor, invites the vendor's
 *   translator (`visibility: assigned`, scoped to the project) and a
 *   reviewer, and assigns two units to the vendor — all through /v1.
 * - The **vendor member** opens My work — the only screen that is
 *   theirs — accepts the assignment, opens it in the editor (scoped to
 *   its two units, none of the third), translates one, and marks it
 *   complete.
 * - The owner asks for that translation to be approved by a reviewer;
 *   the **reviewer** sees it in the approvals inbox with the vendor's
 *   text and name, and approves it.
 */
import { expect, test, type Browser, type Page } from "@playwright/test";
import { api, expectAccessible, signIn } from "./support";

async function person(browser: Browser, email: string): Promise<Page> {
  const page = await (await browser.newContext()).newPage();
  await signIn(page, email);
  return page;
}

test("a vendor member works through an assignment, and a reviewer approves the result", async ({ page, browser }) => {
  test.setTimeout(240_000);
  const run = Date.now();
  const vendorEmail = `vera-${run}@lingua.example`;
  const reviewerEmail = `rita-${run}@example.com`;

  // ── the owner sets the organisation up through the API ──
  await signIn(page, `owner-${run}@example.com`);
  const owner = await api(page);
  const org = await owner.post("/v1/tenants", { slug: `acme-${run}`, name: "Acme" });
  const T = `/v1/tenants/${org.id}`;
  const project = await owner.post(`${T}/projects`, { name: "Portal", slug: "portal", source_locale: "en" });
  const P = `${T}/projects/${project.id}`;
  await owner.post(`${P}/locales`, { code: "de" });
  await owner.post(`${P}/message-upserts`, {
    items: [
      { key: "checkout.pay", text: "Pay now", syntax: "mf2" },
      { key: "checkout.back", text: "Back to cart", syntax: "mf2" },
      { key: "admin.secret", text: "Internal only", syntax: "mf2" },
    ],
  });
  const vendor = await owner.post(`${T}/vendors`, { name: `Lingua ${run}`, contact: "jobs@lingua.example", locales: ["de"] });
  const invited = await owner.post(`${T}/members`, {
    email: vendorEmail,
    roles: ["translator"],
    locales: ["de"],
    vendor_id: vendor.id,
    visibility: "assigned",
    projects: [project.id],
  });
  expect(invited.visibility).toBe("assigned");
  await owner.post(`${T}/members`, { email: reviewerEmail, roles: ["reviewer"], locales: ["de"] });

  // Signing in accepts each invitation.
  const vera = await person(browser, vendorEmail);
  const rita = await person(browser, reviewerEmail);

  const assignment = await owner.post(`${T}/assignments`, {
    project_id: project.id,
    units: [
      { message: "checkout.pay", locale: "de" },
      { message: "checkout.back", locale: "de" },
    ],
    assignee: { vendor: vendor.id },
    due_at: new Date(Date.now() + 7 * 86_400_000).toISOString(),
  });
  expect(assignment.state).toBe("open");

  // ── the vendor member: My work is their screen ──
  await vera.goto(`/t/${org.id}/work`);
  await expect(vera.getByRole("heading", { name: "My work", level: 1 })).toBeVisible();
  // Not a reviewer: no approvals inbox on offer.
  await expect(vera.getByTestId("nav-my-work")).toBeVisible();
  await expect(vera.getByTestId("nav-approvals")).toHaveCount(0);
  const card = vera.locator(`[data-assignment="${assignment.id}"]`);
  await expect(card.getByRole("heading", { name: "Portal" })).toBeVisible();
  await expect(card.getByTestId("assignment-state")).toHaveText("Open");
  await expect(card.getByTestId("assignment-units")).toHaveText("2 units");
  await expect(card.getByTestId("assignment-locales")).toHaveText("In de");
  await expectAccessible(vera, "my work, an open assignment");

  await card.getByRole("button", { name: "Accept" }).click();
  await expect(vera.getByTestId("work-status")).toHaveText("Accepted: the assignment in Portal is yours to work on.");
  await expect(card.getByTestId("assignment-state")).toHaveText("Accepted");
  await expect(card.getByRole("heading", { name: "Portal" })).toBeFocused();

  // The editor, scoped to the assignment: its two units, never the third.
  await card.getByRole("link", { name: "Open in the editor" }).click();
  await expect(vera.getByTestId("assignment-scope")).toContainText("Working on an assignment: 2 units in de");
  const list = vera.getByRole("listbox", { name: "Messages" });
  await expect(list.getByRole("option")).toHaveCount(2);
  await expect(list).not.toContainText("admin.secret");
  await list.getByRole("option", { name: /checkout\.pay/ }).click();
  await expect(vera.getByRole("heading", { name: "checkout.pay" })).toBeVisible();
  const editor = vera.getByTestId("target-editor");
  await editor.click();
  await editor.fill("Jetzt bezahlen");
  await vera.keyboard.press("ControlOrMeta+Enter");
  await expect(vera.getByTestId("editor-status")).toHaveText("Saved.");

  // Back to My work, and done.
  await vera.getByRole("link", { name: "Back to My work" }).click();
  await card.getByRole("button", { name: "Mark complete" }).click();
  await expect(vera.getByTestId("work-status")).toContainText("Marked complete");
  await expect(vera.getByTestId("work-finished").locator(`[data-assignment="${assignment.id}"]`).getByTestId("assignment-state")).toHaveText("Done");
  expect((await owner.get(`${T}/assignments/${assignment.id}`)).state).toBe("done");

  // ── the reviewer approves the vendor's text ──
  const asked = await owner.post(`${T}/approvals`, { project_id: project.id, message: "checkout.pay", locale: "de", n: 1, from: { role: "reviewer" } });
  expect(asked.state).toBe("pending");

  await rita.goto(`/t/${org.id}/approvals`);
  await expect(rita.getByRole("heading", { name: "Approvals", level: 1 })).toBeVisible();
  const approval = rita.locator(`[data-approval="${asked.id}"]`);
  await expect(approval.getByRole("heading", { name: "checkout.pay" })).toBeVisible();
  await expect(approval.getByTestId("approval-state")).toHaveText("Pending");
  await expect(approval.getByTestId("approval-text")).toHaveText("Jetzt bezahlen");
  await expect(approval.getByTestId("approval-author")).toHaveText(`Written by ${vendorEmail}`);
  await expect(approval).toContainText("Four-eyes");
  await expectAccessible(rita, "approvals inbox");

  await approval.getByLabel("Reason (optional)").fill("Matches the glossary");
  await approval.getByRole("button", { name: "Approve" }).click();
  await expect(rita.getByTestId("approvals-status")).toHaveText("Approved checkout.pay (de).");
  await expect(rita.getByTestId("approvals-empty")).toBeVisible();
  const decided = await owner.get(`${T}/approvals/${asked.id}`);
  expect(decided.state).toBe("granted");
  expect(decided.decisions).toEqual([expect.objectContaining({ decision: "granted", reason: "Matches the glossary" })]);
});
