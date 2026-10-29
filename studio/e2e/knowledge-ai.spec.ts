/**
 * M2 end to end (RFC 0003 §6): the termbase feeds term highlights and
 * live terminology QA in the editor, approved text becomes translation
 * memory (inserted in the editor's syntax), "Fill with AI" previews what
 * it would do and then runs the real translation agent in glossa-server
 * against the fake provider (e2e/fake-provider.ts, answering from
 * fixtures/provider-cassette.json — never a real provider), and the
 * review queue triages the suggestions riskiest first, each with its
 * source and no request per item.
 */
import { readFileSync } from "node:fs";
import { expect, test } from "@playwright/test";
import { fakeProviderURL, PROVIDER_LOG } from "./harness";
import { api, expectAccessible, expectAccessibleInBothThemes, signIn } from "./support";

test("terms, translation memory, fill with AI and the review queue", async ({ page }) => {
  test.setTimeout(240_000);
  await signIn(page, `linguist-${Date.now()}@example.com`);

  // ── a project with a small catalog (through the API, as the CLI would) ──
  const v1 = await api(page);
  const t = v1.tenant;
  const project = await v1.post(`/v1/tenants/${t}/projects`, { name: "Knowledge Demo", slug: `knowledge-${Date.now()}`, source_locale: "en" });
  const P = `/v1/tenants/${t}/projects/${project.id}`;
  await v1.post(`${P}/locales`, { code: "de" });
  await v1.post(`${P}/message-upserts`, {
    items: [
      { key: "workspace.create", text: "Create a workspace", description: "Button on the empty dashboard" },
      { key: "workspace.create_new", text: "Create a new workspace", description: "Menu item" },
      { key: "workspace.delete", text: "Delete this workspace", description: "Danger zone button" },
      { key: "billing.invoice", text: "Download your invoice" },
      { key: "terms.accept", namespace: "legal", text: "By continuing you accept the terms." },
    ],
  });
  await expect.poll(async () => (await v1.get(`${P}/messages?missing_in=de`)).items.length).toBe(5);
  const base = `/t/${t}/p/${project.id}`;

  // ── the termbase: one concept, the German term to use and the one to avoid ──
  await page.goto(`${base}/terms`);
  await expect(page.getByRole("heading", { name: "Termbase", level: 1 })).toBeVisible();
  await page.getByRole("button", { name: "New concept" }).click();
  let d = page.getByRole("dialog", { name: "New concept" });
  await d.getByLabel("Definition").fill("A shared environment that holds projects and their members.");
  await d.getByLabel("Term 1", { exact: true }).fill("workspace");
  await expect(d.getByLabel("Locale 2", { exact: true })).toHaveValue("de");
  await d.getByLabel("Term 2", { exact: true }).fill("Arbeitsbereich");
  await d.getByRole("button", { name: "Add term" }).click();
  await d.getByLabel("Locale 3", { exact: true }).selectOption("de");
  await d.getByLabel("Term 3", { exact: true }).fill("Workspace");
  await d.getByLabel("Status 3", { exact: true }).selectOption("forbidden");
  await d.getByLabel("Case-sensitive 3", { exact: true }).check();
  await expectAccessible(page, "concept dialog");
  await d.getByRole("button", { name: "Save concept" }).click();
  await expect(page.getByTestId("termbase-status")).toHaveText("Concept added.");
  const concept = page.getByTestId("concepts").getByRole("listitem").first();
  await expect(concept).toContainText("Arbeitsbereich");
  await expect(concept).toContainText("Forbidden");
  await expectAccessibleInBothThemes(page, "termbase");

  // ── the term is highlighted in the source; the forbidden one is flagged as you type ──
  await page.goto(`${base}/translate?locale=de&key=workspace.create`);
  await expect(page.getByRole("heading", { name: "workspace.create" })).toBeVisible();
  await expect(page.getByTestId("term-highlight")).toHaveText("workspace");
  const terms = page.getByTestId("terms-pane");
  await expect(terms).toContainText("Arbeitsbereich");
  await expect(terms).toContainText("Forbidden — avoid");
  const editor = page.getByTestId("target-editor");
  await page.keyboard.press("Enter");
  await expect(editor).toBeFocused();
  await editor.fill("Einen Workspace erstellen");
  const findings = page.getByTestId("term-findings");
  await expect(findings).toContainText("“Workspace” is a term to avoid here.");
  await expect(findings).toContainText("Use: Arbeitsbereich");
  await expect(editor).toHaveAttribute("aria-describedby", /term-findings/);
  await expectAccessibleInBothThemes(page, "workspace with a terminology finding");
  await editor.fill("Einen Arbeitsbereich erstellen");
  await expect(findings).toBeHidden();
  await page.keyboard.press("ControlOrMeta+Shift+Enter");
  await expect(page.getByTestId("translation-state")).toHaveText("Approved");

  // ── the approval became translation memory: a fuzzy match for the similar message ──
  const list = page.getByRole("listbox", { name: "Messages" });
  const tm = page.getByTestId("tm-pane");
  await expect(async () => {
    await list.getByRole("option", { name: /billing\.invoice/ }).click();
    await list.getByRole("option", { name: /workspace\.create_new/ }).click();
    await expect(tm.getByTestId("tm-match").first()).toContainText("Einen Arbeitsbereich erstellen", { timeout: 1000 });
  }).toPass({ timeout: 15_000 });
  await expect(tm.getByTestId("tm-score").first()).toHaveText(/^(5\d|[6-9]\d)$/);
  await expect(tm).toContainText("Fuzzy match");
  await page.keyboard.press("Enter");
  await page.keyboard.press("ControlOrMeta+Alt+1");
  await expect(editor).toHaveValue("Einen Arbeitsbereich erstellen");
  await expect(editor).toBeFocused();
  // The match came in the editor's syntax (MF1, the project's default), so the translation stays MF1.
  await expect(page.locator("#target-syntax")).toHaveValue("mf1");

  // ── AI settings: a provider (the fake one), consent, a budget, routing ──
  await page.getByRole("link", { name: "AI", exact: true }).click();
  await expect(page.getByRole("heading", { name: "AI", level: 1 })).toBeVisible();
  await expect(page.getByTestId("consent-state")).toHaveText("Sending text to AI providers is off.");
  await page.getByRole("button", { name: "Add provider" }).click();
  d = page.getByRole("dialog", { name: "Add provider" });
  await d.getByLabel("Name").fill("fake-llm");
  await d.getByLabel("API", { exact: true }).selectOption("openai_compatible");
  await d.getByLabel("Endpoint (base URL)").fill(fakeProviderURL);
  await d.getByLabel("Allowed models").fill("fake-model");
  await d.getByLabel("API key").fill("sk-e2e-never-shown");
  await expect(d).toContainText("Stored sealed, never shown again");
  await d.getByRole("button", { name: "Save" }).click();
  const providerRow = page.locator("tr[data-provider=fake-llm]");
  await expect(providerRow).toContainText("Key stored (sealed)");
  expect(await page.content()).not.toContain("sk-e2e-never-shown");

  await page.getByRole("button", { name: "Allow sending text…" }).click();
  d = page.getByRole("dialog", { name: "Allow sending text to AI providers?" });
  await expectAccessible(page, "consent dialog");
  await d.getByRole("button", { name: "Allow", exact: true }).click();
  await expect(page.getByTestId("consent-state")).toHaveText("Sending text to AI providers is allowed.");

  await page.getByLabel("Monthly budget (USD)").fill("5");
  await page.getByRole("button", { name: "Save budget" }).click();
  await expect(page.getByTestId("ai-settings-status")).toHaveText("Budget saved.");
  await expect(page.getByTestId("budget-spent")).toHaveText("$0.00 of $5.00 spent this month");

  // Routing to the fake provider for every task (the default routes to Anthropic).
  await v1.put(`/v1/tenants/${t}/ai-routing-policy`, {
    rules: ["translate", "review", "explain", "assess"].map((task) => ({ task, routes: [{ provider: "fake-llm", model: "fake-model", max_tokens: 2048 }] })),
  });
  await page.reload();
  await expect(page.getByTestId("routing-source")).toHaveText("The workspace's policy applies.");
  await expect(page.getByTestId("routing").getByLabel("Model 1").first()).toHaveValue("fake-model");

  // The legal namespace is riskier: its suggestions are reviewed first.
  const policy = page.getByTestId("project-policy");
  await policy.getByLabel("Namespace", { exact: true }).fill("legal");
  await policy.getByRole("button", { name: "Add namespace" }).click();
  await policy.getByLabel("Legal: legal").check();
  await expect(policy.getByTestId("auto-approve")).not.toBeChecked();
  await policy.getByRole("button", { name: "Save project policy" }).click();
  await expect(page.getByTestId("ai-settings-status")).toHaveText("Project policy saved.");
  await expectAccessibleInBothThemes(page, "AI settings");

  // ── Fill with AI: the agent runs in the server against the cassette ──
  await page.getByRole("link", { name: "Translate" }).click();
  await page.getByLabel("Target locale").selectOption("de");
  await page.getByRole("button", { name: "Fill with AI…" }).click();
  d = page.getByRole("dialog", { name: "Fill de with AI" });
  await expect(d).toContainText("Every message selected below gets an AI suggestion in de.");
  await expect(d.getByRole("radio", { name: "Missing (none yet, or rejected)" })).toBeChecked();
  // The server's preview first: nothing is queued until it's confirmed.
  const preview = d.getByTestId("fill-preview");
  await expect(preview.getByTestId("fill-plan-messages")).toHaveText("4 messages to fill:");
  await expect(preview.getByTestId("fill-plan")).toContainText("4 call an AI provider");
  await expect(preview.getByTestId("fill-cost")).toContainText(/^Estimated cost \$[\d.]+; at most \$[\d.]+/);
  await preview.getByText("Show the 4 keys (de)").click();
  await expect(preview.locator("details li")).toHaveText(["billing.invoice", "terms.accept", "workspace.create_new", "workspace.delete"]);
  expect((await v1.get(`/v1/tenants/${t}/ai-jobs?project=${project.id}`)).items).toEqual([]);
  await expectAccessible(page, "fill preview");
  await d.getByRole("button", { name: "Fill 4 messages" }).click();
  await expect(d.getByTestId("fill-status")).toHaveText("Done: 4 suggested, 0 skipped, 0 failed.", { timeout: 60_000 });
  await expect(d.getByTestId("fill-warnings")).toHaveCount(0);
  await expectAccessible(page, "fill dialog");
  await d.getByRole("button", { name: "Close" }).click();
  // The provider saw the termbase: the prompt listed the term to use and the one to avoid.
  const sent = readFileSync(PROVIDER_LOG, "utf8");
  expect(sent).toContain("Delete this workspace");
  expect(sent).toContain("Arbeitsbereich");

  // The suggestion in the editor: a band and a number, and why.
  await list.getByRole("option", { name: /workspace\.delete/ }).click();
  const ai = page.getByTestId("ai-panel");
  await expect(ai.getByTestId("suggestion-text")).toHaveText("Diesen Workspace löschen");
  await expect(ai.getByTestId("confidence-band")).toHaveText("Low confidence");
  await expect(ai.getByTestId("confidence-score")).toHaveText(/^score 0\.\d\d$/);
  await expect(ai.getByTestId("suggestion-action")).toHaveText("Review required");
  await expect(ai).toContainText("Forbidden term");
  await ai.getByText("Why?").click();
  await expect(ai.locator("tr[data-factor=term_forbidden]")).toContainText("−0.");
  await expect(ai.getByTestId("provenance")).toContainText("fake-llm / fake-model");
  await expect(ai.getByTestId("provenance")).toContainText("translate/v1");
  await expectAccessibleInBothThemes(page, "workspace with an AI suggestion");

  // ── the review queue: riskiest first, triaged from the keyboard ──
  // Each item carries its source: moving through the queue sends no message request.
  const messageRequests: string[] = [];
  page.on("request", (r) => {
    if (/\/messages\//.test(new URL(r.url()).pathname)) messageRequests.push(r.url());
  });
  await page.getByRole("link", { name: "Review", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Review queue", level: 1 })).toBeVisible();
  const queue = page.getByTestId("review-list").getByRole("option");
  await expect(queue).toHaveCount(4);
  await expect(queue.first()).toContainText("workspace.delete");
  const scores = await queue.evaluateAll((items) => items.map((li) => Number(/· (\d\.\d\d)/.exec(li.textContent ?? "")?.[1])));
  expect(scores).toEqual([...scores].sort((a, b) => a - b));
  await expect(page.getByTestId("review-detail")).toContainText("Delete this workspace");
  await expect(page.getByTestId("batch-accept")).toHaveText("Accept 2 recommended");
  await expectAccessibleInBothThemes(page, "review queue");

  await page.keyboard.press("j");
  await expect(queue.nth(1)).toHaveAttribute("aria-selected", "true");
  await expect(page.getByTestId("review-source")).not.toHaveText("Delete this workspace");
  await page.keyboard.press("k");
  await expect(queue.first()).toHaveAttribute("aria-selected", "true");
  await expect(page.getByTestId("review-source")).toHaveText("Delete this workspace");
  expect(messageRequests).toEqual([]);
  await page.keyboard.press("e");
  const edit = page.getByTestId("review-edit");
  await expect(edit).toBeFocused();
  await expect(edit).toHaveValue("Diesen Workspace löschen");
  await edit.fill("Diesen Arbeitsbereich löschen");
  await page.keyboard.press("ControlOrMeta+Enter");
  await expect(page.getByTestId("review-status")).toHaveText("Accepted your edit of workspace.delete (de).");
  await expect(queue).toHaveCount(3);

  // Batch accept takes only what the policy recommends approving.
  await page.getByTestId("batch-accept").click();
  d = page.getByRole("dialog", { name: "Accept every recommended suggestion?" });
  await d.getByRole("button", { name: "Accept 2" }).click();
  await expect(page.getByTestId("review-status")).toHaveText("Accepted 2.");
  await expect(queue).toHaveCount(1);
  await expect(queue.first()).toContainText("terms.accept");
  await expect(queue.first()).toContainText("Legal text");

  // The edit is the translation now, with AI provenance.
  await page.goto(`${base}/translate?locale=de&key=workspace.delete`);
  await expect(editor).toHaveValue("Diesen Arbeitsbereich löschen");
  await expect(page.getByTestId("translation-state")).toHaveText("Approved");
  await expect(page.getByText("Origin: ai")).toBeVisible();

  // …and the metrics count it as accepted after an edit.
  await page.getByRole("link", { name: "AI", exact: true }).click();
  const de = page.getByTestId("ai-metrics").locator("tr[data-locale=de]");
  await expect(de).toContainText("100%");
  await expect(page.getByTestId("disclosures").getByRole("listitem").first()).toContainText("fake-llm/fake-model");
});
