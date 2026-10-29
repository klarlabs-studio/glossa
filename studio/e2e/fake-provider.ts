/**
 * A fake AI provider for the e2e stack: an OpenAI-compatible
 * `/chat/completions` endpoint that answers from a cassette
 * (fixtures/provider-cassette.json) instead of a model, so the
 * translation agent runs for real — its prompts, MF2 parsing,
 * validation, repairs, scoring and routing — without a real provider,
 * a key or any cost.
 *
 * The translate prompt carries the source between <source> tags; the
 * assess prompt also carries the draft between <translation> tags. A
 * request no cassette entry answers fails loudly (HTTP 400 with the
 * source), so a changed prompt or fixture can't pass by accident.
 * Every request is appended to .state/provider-requests.jsonl.
 */
import { appendFileSync, readFileSync } from "node:fs";
import { createServer, type Server } from "node:http";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));

interface Cassette {
  model: string;
  drafts: Array<{ source: string; target_locale: string; message: string; notes?: string }>;
  assessments: Array<{ translation: string; score: number; formality_ok: boolean; issues: string[] }>;
}

interface ChatRequest {
  model: string;
  messages: Array<{ role: string; content: string }>;
}

const between = (text: string, tag: string): string | undefined => {
  const m = new RegExp(`<${tag}>\\n?([\\s\\S]*?)\\n?</${tag}>`).exec(text);
  return m?.[1]?.trim();
};

export function answer(cassette: Cassette, req: ChatRequest): { status: number; content: string } {
  const user = req.messages.filter((m) => m.role === "user").at(-1)?.content ?? "";
  const source = between(user, "source");
  const translation = between(user, "translation");
  if (translation !== undefined) {
    const a = cassette.assessments.find((x) => x.translation === translation);
    if (!a) return { status: 400, content: `cassette miss (assess): ${JSON.stringify(translation)}` };
    return { status: 200, content: JSON.stringify({ score: a.score, formality_ok: a.formality_ok, issues: a.issues }) };
  }
  const target = /from \S+ to (\S+?)\.?\n/.exec(user)?.[1];
  const d = cassette.drafts.find((x) => x.source === source && (!target || x.target_locale === target));
  if (!d) return { status: 400, content: `cassette miss (translate ${target}): ${JSON.stringify(source)}` };
  return { status: 200, content: JSON.stringify({ message: d.message, notes: d.notes ?? "" }) };
}

export async function startFakeProvider(port: number, logFile: string): Promise<Server> {
  const cassette = JSON.parse(readFileSync(join(here, "fixtures", "provider-cassette.json"), "utf8")) as Cassette;
  const server = createServer((request, response) => {
    let body = "";
    request.on("data", (chunk) => (body += chunk));
    request.on("end", () => {
      appendFileSync(logFile, `${JSON.stringify({ url: request.url, auth: request.headers.authorization ? "set" : "none", body: safeJSON(body) })}\n`);
      if (request.method !== "POST" || !request.url?.endsWith("/chat/completions")) {
        response.writeHead(404).end();
        return;
      }
      const req = safeJSON(body) as ChatRequest | undefined;
      const a = req ? answer(cassette, req) : { status: 400, content: "not JSON" };
      if (a.status !== 200) {
        response.writeHead(a.status, { "content-type": "application/json" }).end(JSON.stringify({ error: { message: a.content } }));
        return;
      }
      response.writeHead(200, { "content-type": "application/json" }).end(
        JSON.stringify({
          model: req?.model ?? cassette.model,
          choices: [{ message: { role: "assistant", content: a.content }, finish_reason: "stop" }],
          usage: { prompt_tokens: 900, completion_tokens: 40 },
        }),
      );
    });
  });
  await new Promise<void>((resolve) => server.listen(port, "127.0.0.1", resolve));
  return server;
}

function safeJSON(s: string): unknown {
  try {
    return JSON.parse(s);
  } catch {
    return undefined;
  }
}
