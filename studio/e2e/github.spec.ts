/**
 * Connecting GitHub end to end (RFC 0004 §6): the workspace's GitHub
 * settings screen against a real glossa-server with a real GitHub App
 * configured, talking to the fake GitHub on loopback.
 *
 * The whole install round trip runs: the screen asks the server for a
 * single-use state and is sent to GitHub's install URL; the test plays
 * GitHub's part and returns to the callback with the state, an
 * installation id and a one-time code; the server redeems that code for
 * the person's own token and checks with it that they can see the
 * installation before mapping it to the workspace. Then a repository is
 * connected to a project and application, and disconnected again.
 *
 * Both states — empty and connected — are checked with axe in both
 * themes.
 */
import { expect, test } from "@playwright/test";
import { ACCOUNT, INSTALLATION_ID, OAUTH_CODE, REPOSITORY } from "./fake-github";
import { api, expectAccessibleInBothThemes, signIn } from "./support";

test("connect a GitHub account, tie a repository to a project, and disconnect it", async ({ page }) => {
  test.setTimeout(180_000);
  await signIn(page, `github-${Date.now()}@example.com`);

  // ── a project with one application to connect a repository to ──
  const v1 = await api(page);
  const t = v1.tenant;
  const project = await v1.post(`/v1/tenants/${t}/projects`, { name: "GitHub Demo", slug: `gh-${Date.now()}`, source_locale: "en" });
  await v1.post(`/v1/tenants/${t}/projects/${project.id}/applications`, { slug: "web", name: "Web", platform: "web" });
  const settings = `/t/${t}/settings/github`;

  // ── nothing connected yet ──
  await page.goto(settings);
  await expect(page.getByRole("heading", { name: "GitHub", level: 1 })).toBeVisible();
  await expect(page.getByTestId("github-no-installations")).toHaveText("No GitHub account is connected yet.");
  await expect(page.getByTestId("github-no-connections")).toHaveText("No repository is connected yet.");
  // Nothing to connect a repository to, so that button is inert.
  await expect(page.getByTestId("github-add")).toBeDisabled();
  await expectAccessibleInBothThemes(page, "github settings, empty");

  // ── the install round trip ──
  // "Connect" sends the person to GitHub; the fake has no install page,
  // so the test plays GitHub's part and returns with what it would send.
  const install = page.waitForRequest((r) => r.url().includes("/github/install-intents"));
  await page.getByTestId("github-connect").click();
  await install;
  await expect.poll(() => page.url()).toContain("/apps/glossa-e2e/installations/new");
  const state = new URL(page.url()).searchParams.get("state");
  expect(state).toBeTruthy();

  await page.goto(`${settings}?state=${state}&installation_id=${INSTALLATION_ID}&code=${OAUTH_CODE}&setup_action=install`);
  await expect(page.getByTestId("github-installations")).toContainText(ACCOUNT);
  await expect(page.getByTestId("github-installations")).toContainText(REPOSITORY);
  // The single-use state is off the URL, so a reload cannot replay it.
  await expect.poll(() => new URL(page.url()).search).toBe("");

  // ── connect the repository to the project ──
  await page.getByTestId("github-add").click();
  const form = page.getByRole("form", { name: "Connect a repository" });
  await form.getByLabel("Repository", { exact: true }).selectOption({ label: REPOSITORY });
  await form.getByLabel("Project", { exact: true }).selectOption({ label: "GitHub Demo" });
  await form.getByLabel("Application", { exact: true }).selectOption({ label: "Web" });
  await form.getByLabel("Path", { exact: true }).fill("apps/web");
  await page.getByTestId("github-add-confirm").click();

  const connections = page.getByTestId("github-connections");
  await expect(connections).toContainText(REPOSITORY);
  await expect(connections).toContainText("apps/web");
  await expect(connections).toContainText("GitHub Demo");
  await expect(connections).toContainText("main");
  await expectAccessibleInBothThemes(page, "github settings, connected");

  // The server stored it under the workspace, keyed by the numeric id.
  const stored = await v1.get(`/v1/tenants/${t}/github/connections`);
  expect(stored.items).toHaveLength(1);
  expect(stored.items[0]).toMatchObject({ repository_name: REPOSITORY, path: "apps/web", default_branch: "main", project_id: project.id });

  // ── disconnecting unlinks locally and leaves GitHub alone ──
  await page.getByRole("button", { name: `Disconnect: ${REPOSITORY}` }).click();
  await expect(page.getByRole("dialog")).toContainText("Nothing changes on GitHub");
  await page.getByTestId("github-remove-confirm").click();
  await expect(page.getByTestId("github-no-connections")).toBeVisible();
  // The account stays connected: only the link went.
  await expect(page.getByTestId("github-installations")).toContainText(ACCOUNT);
});
