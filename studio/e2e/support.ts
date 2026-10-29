/** Helpers the e2e specs share: axe in both themes, screenshots, signing in. */
import AxeBuilder from "@axe-core/playwright";
import { expect, type Page } from "@playwright/test";
import { signInLink } from "./harness";

/** With STUDIO_SCREENSHOTS=<dir>, keeps a full-page screenshot of each screen checked (for design review). */
export async function snap(page: Page, name: string): Promise<void> {
  const dir = process.env.STUDIO_SCREENSHOTS;
  if (dir) await page.screenshot({ path: `${dir}/${name}.png`, fullPage: true });
}

/** Switch the theme and wait out the colour transitions, so axe measures the settled colours. */
export async function setTheme(page: Page, theme: "light" | "dark"): Promise<void> {
  await page.evaluate(async (t) => {
    document.documentElement.setAttribute("data-theme", t);
    await new Promise((r) => requestAnimationFrame(r));
    await Promise.all(document.getAnimations().map((a) => a.finished.catch(() => undefined)));
  }, theme);
}

/** WCAG 2.2 AA through axe; any violation fails with its rule ids and targets. */
export async function expectAccessible(page: Page, screen: string): Promise<void> {
  const { violations } = await new AxeBuilder({ page }).withTags(["wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"]).analyze();
  const summary = violations.map((v) => `${v.id}: ${v.nodes.map((n) => n.target.join(" ")).join(", ")}`);
  expect(summary, `axe violations on ${screen}`).toEqual([]);
  await snap(page, screen.replace(/\W+/g, "-"));
}

/** Axe in the dark theme too, then back to light. */
export async function expectAccessibleInBothThemes(page: Page, screen: string): Promise<void> {
  await expectAccessible(page, screen);
  await setTheme(page, "dark");
  await expectAccessible(page, `${screen} (dark)`);
  await setTheme(page, "light");
}

/** Sign in through the magic link the log mailer captured; lands on Projects. */
export async function signIn(page: Page, email: string): Promise<void> {
  await page.goto("/app");
  await page.getByLabel("Email address").fill(email);
  await page.getByRole("button", { name: "Email me a sign-in link" }).click();
  await expect(page.getByRole("heading", { name: "Check your inbox" })).toBeVisible();
  await page.goto(await signInLink(email));
  await expect(page.getByRole("heading", { name: "Projects" })).toBeVisible();
}

/** The API as the signed-in person: same cookie, plus the session's CSRF token on writes. */
export async function api(page: Page) {
  const me = await (await page.request.get("/v1/me")).json();
  const headers = { "X-CSRF-Token": me.csrf_token as string };
  // Test code reads the JSON it just asked for; the contract types add nothing here.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const check = async (r: Awaited<ReturnType<Page["request"]["get"]>>): Promise<any> => {
    expect(r.ok(), `${r.url()}: ${r.status()} ${await r.text()}`).toBe(true);
    return r.status() === 204 ? undefined : r.json();
  };
  return {
    tenant: me.person.individual_tenant_id as string,
    get: async (path: string) => check(await page.request.get(path)),
    post: async (path: string, data: unknown, extra: Record<string, string> = {}) => check(await page.request.post(path, { headers: { ...headers, ...extra }, data })),
    put: async (path: string, data: unknown) => check(await page.request.put(path, { headers, data })),
    /** A multipart upload, as CI sends captures: the manifest part first, then one part per image. */
    postMultipart: async (path: string, multipart: Record<string, string | { name: string; mimeType: string; buffer: Buffer }>) =>
      check(await page.request.post(path, { headers, multipart })),
  };
}
