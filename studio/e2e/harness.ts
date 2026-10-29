/**
 * The e2e stack, provisioned the way production is (platform/README.md):
 * Postgres 16 in a testcontainer, a CREATEROLE owner that runs the
 * migrations, the server connecting as the non-superuser `glossa_app`,
 * the log mailer, whose captured sign-in links the tests follow, and
 * release object storage on the `dir` driver in a fresh directory, which
 * the tests read the way glossa-edge would.
 */
import { spawn, spawnSync, type ChildProcess } from "node:child_process";
import { createHash, generateKeyPairSync, randomBytes } from "node:crypto";
import { createWriteStream, existsSync, mkdirSync, readFileSync, rmSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { PostgreSqlContainer, type StartedPostgreSqlContainer } from "@testcontainers/postgresql";
import { startFakeGitHub } from "./fake-github";
import { startFakeProvider } from "./fake-provider";

const here = dirname(fileURLToPath(import.meta.url));
export const STATE_DIR = join(here, ".state");
/**
 * Where the server's log is, and so where the sign-in links are read
 * from. The M4 exit test (RFC 0005 §12.8) drives this suite against a
 * glossa-server it started itself, and points this at that server's log.
 */
export const SERVER_LOG = process.env.GLOSSA_E2E_SERVER_LOG ?? join(STATE_DIR, "server.log");
/** Release object storage (GLOSSA_STORAGE_DRIVER=dir), laid out as platform/internal/release/delivery says. */
export const OBJECTS_DIR = join(STATE_DIR, "objects");
const PLATFORM = resolve(here, "../../platform");
/** Every request the fake AI provider received, one JSON object per line. */
export const PROVIDER_LOG = join(STATE_DIR, "provider-requests.jsonl");

export const ports = {
  api: Number(process.env.GLOSSA_E2E_API_PORT ?? 18317),
  studio: Number(process.env.GLOSSA_E2E_STUDIO_PORT ?? 4317),
  provider: Number(process.env.GLOSSA_E2E_PROVIDER_PORT ?? 18318),
  github: Number(process.env.GLOSSA_E2E_GITHUB_PORT ?? 18319),
};
export const studioURL = `http://localhost:${ports.studio}`;
/**
 * The glossa-server the suite talks to. Normally the one `startStack`
 * provisions below; with GLOSSA_E2E_API_URL set, one somebody else is
 * running — which is how the M4 exit test opens Studio's `quality` view
 * "against the same server" (RFC 0005 §12.8) rather than against a
 * second one with different data in it.
 */
export const externalAPI = process.env.GLOSSA_E2E_API_URL?.replace(/\/$/, "");
export const apiURL = externalAPI ?? `http://127.0.0.1:${ports.api}`;
/** The fake AI provider's base URL, as a tenant configures it (an OpenAI-compatible endpoint on loopback). */
export const fakeProviderURL = `http://127.0.0.1:${ports.provider}/v1`;

const OWNER_PASSWORD = "owner-e2e";
const APP_PASSWORD = "app-e2e";

async function psql(pg: StartedPostgreSqlContainer, user: string, password: string, sql: string): Promise<void> {
  const r = await pg.exec(["psql", "-v", "ON_ERROR_STOP=1", "-U", user, "-d", "glossa", "-c", sql], { env: { PGPASSWORD: password } });
  if (r.exitCode !== 0) throw new Error(`psql failed (${sql}): ${r.output}`);
}

function buildServer(): string {
  const bin = join(STATE_DIR, process.platform === "win32" ? "glossa-server.exe" : "glossa-server");
  if (process.env.GLOSSA_E2E_SERVER_BIN) return process.env.GLOSSA_E2E_SERVER_BIN;
  const r = spawnSync("go", ["build", "-o", bin, "./cmd/glossa-server"], { cwd: PLATFORM, stdio: "inherit" });
  if (r.status !== 0) throw new Error("go build ./cmd/glossa-server failed");
  return bin;
}

async function waitFor(url: string, server: ChildProcess, timeoutMs = 60_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (server.exitCode !== null) throw new Error(`glossa-server exited with ${server.exitCode}; see ${SERVER_LOG}`);
    try {
      if ((await fetch(url)).ok) return;
    } catch {
      // not up yet
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error(`${url} not ready after ${timeoutMs} ms; see ${SERVER_LOG}`);
}

export async function startStack(): Promise<() => Promise<void>> {
  // Somebody else's server: provision nothing, tear nothing down, and
  // leave its data exactly as it was handed over.
  if (externalAPI) {
    if (!existsSync(SERVER_LOG)) throw new Error(`GLOSSA_E2E_API_URL is set but ${SERVER_LOG} is not there; set GLOSSA_E2E_SERVER_LOG`);
    return async () => undefined;
  }
  mkdirSync(STATE_DIR, { recursive: true });
  rmSync(OBJECTS_DIR, { recursive: true, force: true });
  mkdirSync(OBJECTS_DIR, { recursive: true });
  rmSync(PROVIDER_LOG, { force: true });
  const bin = buildServer();

  const pg = await new PostgreSqlContainer("postgres:16-alpine").withDatabase("glossa").withUsername("postgres").withPassword("postgres").start();
  await psql(pg, "postgres", "postgres", `CREATE ROLE glossa_owner LOGIN CREATEROLE NOSUPERUSER NOBYPASSRLS PASSWORD '${OWNER_PASSWORD}'`);
  await psql(pg, "postgres", "postgres", "ALTER DATABASE glossa OWNER TO glossa_owner");

  const host = pg.getHost();
  const port = pg.getPort();
  const dsn = (user: string, password: string) => `postgres://${user}:${password}@${host}:${port}/glossa?sslmode=disable`;
  const env = {
    ...process.env,
    DATABASE_URL: dsn("glossa_app", APP_PASSWORD),
    MIGRATION_DATABASE_URL: dsn("glossa_owner", OWNER_PASSWORD),
    GLOSSA_AUTH_SECRET: randomBytes(32).toString("base64"),
    GLOSSA_HTTP_ADDR: `127.0.0.1:${ports.api}`,
    GLOSSA_STUDIO_URL: studioURL,
    GLOSSA_MAIL_DRIVER: "log",
    GLOSSA_LOG_LEVEL: "info",
    GLOSSA_OUTBOX_POLL_INTERVAL: "100ms",
    GLOSSA_SHUTDOWN_TIMEOUT: "5s",
    GLOSSA_STORAGE_DRIVER: "dir",
    GLOSSA_STORAGE_DIR: OBJECTS_DIR,
    // AI jobs run in the server against the fake provider on loopback,
    // which a tenant may configure only where private endpoints are allowed.
    GLOSSA_AI_ALLOW_PRIVATE_ENDPOINTS: "true",
    GLOSSA_AI_POLL_INTERVAL: "200ms",
    // Import and export jobs are picked up quickly, so the specs don't wait out the 1 s default.
    GLOSSA_INTEGRATION_POLL_INTERVAL: "200ms",
    // A GitHub App against the fake on loopback (RFC 0004 §6). The key
    // is generated here and never leaves the run: nothing in the repo
    // is App key material.
    GLOSSA_GITHUB_APP_ID: "99001",
    GLOSSA_GITHUB_APP_SLUG: "glossa-e2e",
    GLOSSA_GITHUB_APP_PRIVATE_KEY: generateKeyPairSync("rsa", { modulusLength: 2048 }).privateKey.export({ type: "pkcs8", format: "pem" }).toString(),
    GLOSSA_GITHUB_WEBHOOK_SECRET: randomBytes(32).toString("hex"),
    GLOSSA_GITHUB_CLIENT_ID: "Iv1.e2e",
    GLOSSA_GITHUB_CLIENT_SECRET: randomBytes(16).toString("hex"),
    GLOSSA_GITHUB_API_URL: `http://127.0.0.1:${ports.github}`,
    GLOSSA_GITHUB_WEB_URL: `http://127.0.0.1:${ports.github}`,
    GLOSSA_GITHUB_INBOX_POLL_INTERVAL: "200ms",
  };

  const migrate = spawnSync(bin, ["-migrate=only"], { env, encoding: "utf8" });
  if (migrate.status !== 0) throw new Error(`migration failed:\n${migrate.stdout}\n${migrate.stderr}`);
  await psql(pg, "glossa_owner", OWNER_PASSWORD, `ALTER ROLE glossa_app LOGIN PASSWORD '${APP_PASSWORD}'`);

  const provider = await startFakeProvider(ports.provider, PROVIDER_LOG);
  const github = await startFakeGitHub(ports.github);
  const log = createWriteStream(SERVER_LOG, { flags: "w" });
  const server = spawn(bin, [], { env, stdio: ["ignore", "pipe", "pipe"] });
  server.stdout?.pipe(log);
  server.stderr?.pipe(log);
  await waitFor(`${apiURL}/readyz`, server);

  return async () => {
    server.kill("SIGTERM");
    await new Promise((r) => (server.exitCode !== null ? r(null) : server.once("exit", r)));
    log.end();
    await new Promise((r) => provider.close(r));
    await new Promise((r) => github.close(r));
    await pg.stop();
  };
}

/** The newest sign-in link the log mailer captured for `email`. */
export async function signInLink(email: string, timeoutMs = 10_000): Promise<string> {
  const deadline = Date.now() + timeoutMs;
  const re = /(https?:\/\/[^\s"\\]+\/auth\/sign-in#token=[A-Za-z0-9_-]+)/g;
  while (Date.now() < deadline) {
    if (existsSync(SERVER_LOG)) {
      const lines = readFileSync(SERVER_LOG, "utf8").split("\n").filter((l) => l.includes(email) && l.includes("#token="));
      const last = lines.at(-1);
      const links = last ? [...last.matchAll(re)].map((m) => m[1]!) : [];
      if (links.length) return links.at(-1)!;
    }
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`no sign-in link for ${email} in ${SERVER_LOG}`);
}

/** The manifest an environment serves, as glossa-edge would read it from storage; null while there is none. */
export function servedManifest(project: string, environment: string): { release: { version: number } } | null {
  const file = join(OBJECTS_DIR, "v1", "projects", project, "environments", environment, "manifest.json");
  return existsSync(file) ? JSON.parse(readFileSync(file, "utf8")) : null;
}

/** Whether storage holds the index object through which the edge resolves a delivery key (present while it is active). */
export function keyIndexed(key: string): boolean {
  return existsSync(join(OBJECTS_DIR, "v1", "keys", `${createHash("sha256").update(key).digest("hex")}.json`));
}
