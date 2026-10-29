/**
 * The workspace's GitHub screen (RFC 0004 §6): the install round trip,
 * the account list, and connecting and disconnecting a repository.
 */
import { flushPromises, type DOMWrapper, type VueWrapper } from "@vue/test-utils";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { connection, createFakeGitHub, installation, type FakeGitHub } from "../../test/fake-github";
import { mountTenantScreen } from "../../test/project";
import GitHubView from "./GitHubView.vue";

let wrapper: VueWrapper | undefined;
let assign: ReturnType<typeof vi.fn>;

const AT = "2026-09-01T00:00:00Z";
const PROJECTS = {
  "/v1/tenants/t/projects": {
    items: [
      { id: "p1", slug: "shop", name: "Shop", source_locale: "en", settings: { default_syntax: "mf2", review_required: false }, created_at: AT, updated_at: AT },
    ],
  },
  "/v1/tenants/t/projects/p1/applications": {
    items: [{ id: "a1", slug: "web", name: "web", platform: "web", created_at: AT, updated_at: AT }],
  },
};

beforeEach(() => {
  assign = vi.fn();
  // jsdom/happy-dom refuse a real navigation; the screen only needs to
  // have asked for one.
  vi.stubGlobal("location", { ...window.location, assign });
});
afterEach(() => {
  wrapper?.unmount();
  wrapper = undefined;
});

const button = (root: { findAll(s: string): DOMWrapper<Element>[] }, name: string): DOMWrapper<Element> => {
  const found = root.findAll("button").find((b) => b.text().trim() === name || b.attributes("aria-label")?.startsWith(name));
  if (!found) throw new Error(`no button ${name}; saw ${root.findAll("button").map((b) => b.text().trim()).join(", ")}`);
  return found;
};

const mount = (fake: FakeGitHub, path = "/t/t/settings/github") =>
  mountTenantScreen(GitHubView, { github: fake, path, responses: PROJECTS, roles: ["admin"] });

describe("GitHubView", () => {
  it("says so plainly when the deployment has no GitHub app, and offers nothing that cannot work", async () => {
    const fake = createFakeGitHub({ failWith: "github_not_configured" });
    wrapper = await mount(fake);

    expect(wrapper.get('[data-testid="github-unconfigured"]').text()).toContain("no GitHub app configured");
    expect(wrapper.find('[data-testid="github-connect"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="github-installations"]').exists()).toBe(false);
  });

  it("shows the empty state before anything is connected", async () => {
    wrapper = await mount(createFakeGitHub());

    expect(wrapper.get('[data-testid="github-no-installations"]').text()).toContain("No GitHub account is connected");
    expect(wrapper.get('[data-testid="github-no-connections"]').text()).toContain("No repository is connected");
    // Nothing to connect a repository to yet, so the button is inert.
    expect(wrapper.get('[data-testid="github-add"]').attributes("disabled")).toBeDefined();
  });

  it("sends the person to GitHub with the state the server issued", async () => {
    const fake = createFakeGitHub();
    wrapper = await mount(fake);

    await button(wrapper, "Connect a GitHub account").trigger("click");
    await flushPromises();

    expect(fake.calls.map((c) => c[0])).toContain("startInstall");
    expect(assign).toHaveBeenCalledWith(expect.stringContaining("/apps/glossa/installations/new?state=state-1"));
  });

  it("finishes the installation from GitHub's callback and clears the query, so a reload cannot replay it", async () => {
    const fake = createFakeGitHub();
    // The state the server would have issued before the round trip.
    const intent = await fake.startInstall("t");
    wrapper = await mount(fake, `/t/t/settings/github?state=${intent.state}&installation_id=4242&code=code-1&setup_action=install`);
    await flushPromises();

    const call = fake.calls.find((c) => c[0] === "completeInstall");
    expect(call?.[2]).toEqual({ state: intent.state, installation_id: 4242, code: "code-1", setup_action: "install" });
    expect(wrapper.get('[data-testid="github-installations"]').text()).toContain("acme");
    // The URL no longer carries the single-use state.
    expect(wrapper.vm.$route.query).toEqual({});
  });

  it("reports a state another workspace already used, without naming it", async () => {
    const fake = createFakeGitHub({ installationList: [installation()] });
    const intent = await fake.startInstall("t");
    wrapper = await mount(fake, `/t/t/settings/github?state=${intent.state}&installation_id=4242&code=code-1&setup_action=install`);
    await flushPromises();

    const alert = wrapper.get('[role="alert"]').text();
    expect(alert).toContain("already connected");
    expect(alert).not.toContain("t2");
  });

  it("connects a repository to a project and application, filling in GitHub's default branch", async () => {
    const fake = createFakeGitHub({ installationList: [installation()] });
    wrapper = await mount(fake);

    await button(wrapper, "Connect a repository").trigger("click");
    await flushPromises();
    await wrapper.get("#gh-repo").setValue("9001");
    await wrapper.get("#gh-project").setValue("p1");
    await flushPromises();
    await wrapper.get("#gh-application").setValue("a1");
    await wrapper.get("#gh-path").setValue("apps/web");
    await wrapper.get('[data-testid="github-add-confirm"]').trigger("click");
    await flushPromises();

    const made = fake.calls.find((c) => c[0] === "connect")?.[2] as Record<string, unknown>;
    expect(made).toMatchObject({ installation_id: "inst-1", repository_id: 9001, project_id: "p1", application_id: "a1", path: "apps/web" });
    expect(made.default_branch).toBe("main");
    expect(wrapper.get('[data-testid="github-connections"]').text()).toContain("acme/shop");
  });

  it("reports a repository and path that are already connected", async () => {
    const fake = createFakeGitHub({ installationList: [installation()], connectionList: [connection({ path: "" })] });
    wrapper = await mount(fake);

    await button(wrapper, "Connect a repository").trigger("click");
    await flushPromises();
    await wrapper.get("#gh-repo").setValue("9001");
    await wrapper.get("#gh-project").setValue("p1");
    await flushPromises();
    await wrapper.get("#gh-application").setValue("a1");
    await wrapper.get('[data-testid="github-add-confirm"]').trigger("click");
    await flushPromises();

    expect(wrapper.get('[role="alert"]').text()).toContain("already connected");
  });

  it("disconnects a repository after confirming, and says nothing changes on GitHub", async () => {
    const fake = createFakeGitHub({ installationList: [installation()], connectionList: [connection()] });
    wrapper = await mount(fake);

    await button(wrapper, "Disconnect").trigger("click");
    await flushPromises();
    expect(document.body.textContent).toContain("Nothing changes on GitHub");
    await wrapper.get('[data-testid="github-remove-confirm"]').trigger("click");
    await flushPromises();

    expect(fake.calls.some((c) => c[0] === "disconnect")).toBe(true);
    expect(wrapper.find('[data-testid="github-no-connections"]').exists()).toBe(true);
  });

  it("forgets an account, saying the app stays installed on GitHub", async () => {
    const fake = createFakeGitHub({ installationList: [installation()], connectionList: [connection()] });
    wrapper = await mount(fake);

    await button(wrapper, "Forget this account").trigger("click");
    await flushPromises();
    expect(document.body.textContent).toContain("stays installed on GitHub");
    await wrapper.get('[data-testid="github-forget-confirm"]').trigger("click");
    await flushPromises();

    expect(fake.installationList).toHaveLength(0);
    expect(fake.connectionList).toHaveLength(0);
  });

  it("explains a suspended account and one that was uninstalled on GitHub", async () => {
    const fake = createFakeGitHub({
      installationList: [
        installation({ id: "i-s", account_login: "suspendco", state: "suspended", repositories: [], repositories_unavailable: true }),
        installation({ id: "i-r", account_login: "goneco", state: "revoked", repositories: [], repositories_unavailable: true }),
      ],
    });
    wrapper = await mount(fake);

    const text = wrapper.get('[data-testid="github-installations"]').text();
    expect(text).toContain("Suspended on GitHub");
    expect(text).toContain("Unsuspend it");
    expect(text).toContain("Uninstalled on GitHub");
    // Neither can be connected to, so the picker offers nothing.
    expect(wrapper.get('[data-testid="github-add"]').attributes("disabled")).toBeDefined();
  });

  it("still lists an account whose repositories GitHub would not give us", async () => {
    const fake = createFakeGitHub({ installationList: [installation({ repositories: [], repositories_unavailable: true })] });
    wrapper = await mount(fake);

    expect(wrapper.get('[data-testid="github-installations"]').text()).toContain("GitHub could not be reached");
  });

  it("shows a reader what is connected but no way to change it", async () => {
    const fake = createFakeGitHub({ installationList: [installation()], connectionList: [connection()] });
    wrapper = await mountTenantScreen(GitHubView, { github: fake, path: "/t/t/settings/github", responses: PROJECTS, roles: ["translator"] });

    expect(wrapper.get('[data-testid="github-no-manage"]').text()).toContain("needs the manage permission");
    expect(wrapper.get('[data-testid="github-connections"]').text()).toContain("acme/shop");
    expect(wrapper.find('[data-testid="github-connect"]').exists()).toBe(false);
    expect(wrapper.find('[data-testid="github-add"]').exists()).toBe(false);
  });
});
