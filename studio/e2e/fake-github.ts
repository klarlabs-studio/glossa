/**
 * A fake GitHub for the e2e stack (RFC 0004 §6): the handful of
 * endpoints glossa-server's App adapter calls, so the install flow runs
 * for real — a signed state, GitHub's round trip, the OAuth code
 * redeemed for a user token, and the ownership check against that token
 * — without a real App, a real key or a real account.
 *
 * It is deliberately lenient about the App JWT (the adapter's own unit
 * tests verify that against a strict fake); what this one exists to
 * prove is the flow through Studio.
 *
 * The web host and the API host are the same server: `/login/oauth/…`
 * is GitHub's web side, everything else its REST API.
 */
import { createServer, type Server } from "node:http";

export const INSTALLATION_ID = 4242;
export const REPOSITORY_ID = 9001;
export const ACCOUNT = "acme-e2e";
export const REPOSITORY = `${ACCOUNT}/shop`;
/** The code GitHub hands back on the callback; single-use, as GitHub's is. */
export const OAUTH_CODE = "e2e-code-1";
const USER_TOKEN = "gho_e2e_owner";

interface State {
  /** Redeemed codes, so a replay is refused the way GitHub refuses it. */
  spent: Set<string>;
  /** The repositories the installation covers. */
  repositories: Array<{ id: number; name: string; full_name: string; private: boolean; default_branch: string }>;
}

function json(body: unknown, status = 200): { status: number; body: string } {
  return { status, body: JSON.stringify(body) };
}

function route(url: string, method: string, body: string, s: State): { status: number; body: string } {
  const path = new URL(url, "http://fake").pathname;

  // The web side: redeem the install flow's one-time code.
  if (method === "POST" && path === "/login/oauth/access_token") {
    const code = new URLSearchParams(body).get("code") ?? "";
    if (code !== OAUTH_CODE || s.spent.has(code)) {
      return json({ error: "bad_verification_code", error_description: "The code passed is incorrect or expired." });
    }
    s.spent.add(code);
    return json({ access_token: USER_TOKEN, token_type: "bearer", scope: "" });
  }

  // An installation token for the App.
  if (method === "POST" && /^\/app\/installations\/\d+\/access_tokens$/.test(path)) {
    return json({ token: "ghs_e2e_installation", expires_at: new Date(Date.now() + 3_600_000).toISOString() }, 201);
  }

  // What the person's own token can see: the ownership check.
  if (method === "GET" && path === "/user/installations") {
    return json({
      total_count: 1,
      installations: [{ id: INSTALLATION_ID, app_id: 1, account: { id: 77, login: ACCOUNT, type: "Organization" } }],
    });
  }

  // What the installation covers.
  if (method === "GET" && path === "/installation/repositories") {
    return json({ total_count: s.repositories.length, repositories: s.repositories });
  }

  return json({ message: "Not Found", documentation_url: "https://docs.github.com/rest" }, 404);
}

/** Starts the fake on port; close it to stop. */
export function startFakeGitHub(port: number): Promise<Server> {
  const state: State = {
    spent: new Set(),
    repositories: [{ id: REPOSITORY_ID, name: "shop", full_name: REPOSITORY, private: false, default_branch: "main" }],
  };
  const server = createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", () => {
      const { status, body } = route(req.url ?? "/", req.method ?? "GET", Buffer.concat(chunks).toString("utf8"), state);
      res.writeHead(status, { "Content-Type": "application/json", "X-RateLimit-Limit": "5000", "X-RateLimit-Remaining": "4999" });
      res.end(body);
    });
  });
  return new Promise((resolve) => server.listen(port, "127.0.0.1", () => resolve(server)));
}
