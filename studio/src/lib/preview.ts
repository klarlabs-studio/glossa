/**
 * Live preview over the canonical MF2 model with the reference formatter
 * (`@klarlabs-studio/glossa/messageformat`, RFC 0002 §5). It is loaded on demand, so it
 * stays out of the initial bundle. MF2 text is parsed here as the
 * translator types; MF1 is parsed only by the server (there is exactly one
 * MF1 converter, in Go), through POST /v1/message-previews, debounced.
 */

/** Quiet time after the last keystroke before MF1 text goes to the server. */
export const MF1_PREVIEW_DELAY_MS = 250;
/** Wait before asking again after the server's rate limit (429). */
export const MF1_PREVIEW_RETRY_MS = 1500;
import type { FormatError, Message, MessagePart } from "@klarlabs-studio/glossa/messageformat";

type MessageFormatModule = typeof import("@klarlabs-studio/glossa/messageformat");

let loading: Promise<MessageFormatModule> | undefined;

/** The reference formatter, imported once. */
export function loadFormatter(): Promise<MessageFormatModule> {
  loading ??= import("@klarlabs-studio/glossa/messageformat");
  return loading;
}

export interface Segment {
  kind: "text" | "value" | "markup" | "fallback";
  text: string;
}

export interface Preview {
  segments: Segment[];
  errors: FormatError[];
}

const BIDI = /[⁦-⁩]/g;

function segmentOf(part: MessagePart<string>): Segment | undefined {
  const p = part as { type: string; value?: unknown; parts?: Array<{ value: unknown }>; source?: string; kind?: string; name?: string };
  switch (p.type) {
    case "text":
      return { kind: "text", text: String(p.value ?? "") };
    case "bidiIsolation":
      return undefined; // each value renders in its own <bdi>
    case "fallback":
      return { kind: "fallback", text: `{${p.source ?? "?"}}` };
    case "markup": {
      const name = p.name ?? "";
      const text = p.kind === "close" ? `{/${name}}` : p.kind === "standalone" ? `{#${name} /}` : `{#${name}}`;
      return { kind: "markup", text };
    }
    default: {
      const text = p.parts ? p.parts.map((x) => String(x.value)).join("") : String(p.value ?? "");
      return { kind: "value", text: text.replace(BIDI, "") };
    }
  }
}

/** Format a model to display segments. Never throws: problems are in `errors`. */
export function renderPreview(
  mf: Pick<MessageFormatModule, "formatToParts">,
  model: Message,
  locale: string,
  values: Record<string, unknown>,
): Preview {
  const errors: FormatError[] = [];
  let parts: MessagePart<string>[];
  try {
    parts = mf.formatToParts(model, locale, values, { onError: (e) => errors.push(e) });
  } catch (e) {
    return { segments: [], errors: [{ type: "bad-message", message: e instanceof Error ? e.message : String(e) }] };
  }
  const segments: Segment[] = [];
  for (const part of parts) {
    const s = segmentOf(part);
    if (!s) continue;
    const last = segments[segments.length - 1];
    if (s.kind === "text" && last?.kind === "text") last.text += s.text;
    else segments.push(s);
  }
  return { segments, errors };
}

export type ParseResult = { ok: true; model: Message } | { ok: false; error: string };

/** Parse MF2 syntax to the canonical model. */
export function parseMF2(mf: Pick<MessageFormatModule, "parseMF2">, text: string): ParseResult {
  try {
    return { ok: true, model: mf.parseMF2(text) };
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : String(e) };
  }
}
