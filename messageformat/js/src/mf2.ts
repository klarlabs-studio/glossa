/**
 * MF2 syntax in and out of the canonical data model, and formatting through
 * the reference implementation (Studio preview, tooling). Browser runtimes
 * use `@klarlabs-studio/glossa-runtime` instead, which interprets the same data model
 * without a parser.
 */
import {
  MessageDataModelError,
  MessageFormat,
  MessageSyntaxError,
  parseMessage,
  stringifyMessage,
  validate,
} from "messageformat";
import type { MessagePart } from "messageformat";
import { DraftFunctions } from "messageformat/functions";
import type { MessageFunction } from "messageformat/functions";
import { MF1Functions } from "@messageformat/icu-messageformat-1";
import { fromReference, toReference } from "./convert.js";
import type { Message } from "./model.js";
import { messageSchema } from "./schema.js";

export { MessageDataModelError, MessageSyntaxError };
export type { MessageFunction, MessagePart };

/**
 * Parse MF2 syntax into the canonical data model.
 *
 * @throws MessageSyntaxError on a syntax error, or its subclass
 *   MessageDataModelError when the message is well-formed but invalid
 *   (duplicate declarations, missing fallback variant, …).
 */
export function parseMF2(src: string): Message {
  const ref = parseMessage(src);
  validate(ref);
  return fromReference(ref);
}

/** Serialize a canonical message as MF2 syntax. */
export function stringify(message: Message): string {
  return stringifyMessage(toReference(message));
}

/**
 * Check that `value` is a well-formed canonical message: the JSON shape of
 * `message.schema.json`, and free of data model errors.
 *
 * @throws Error describing the first shape problem, or MessageDataModelError.
 */
export function validateMessage(value: unknown): Message {
  const res = messageSchema.safeParse(value);
  if (!res.success) throw new Error(`Invalid MessageFormat 2 data model: ${res.error.message}`);
  validate(toReference(res.data));
  return res.data;
}

/** An error reported while formatting; formatting itself never throws. */
export interface FormatError {
  type: string;
  message: string;
  source?: string;
}

export interface FormatOptions {
  /** `"default"` wraps placeholders in Unicode bidi isolates as the spec requires. */
  bidiIsolation?: "default" | "none";
  /** The message's base direction; derived from the locale when omitted. */
  dir?: "ltr" | "rtl" | "auto";
  /** Called once per resolution or formatting error. */
  onError?: (error: FormatError) => void;
  /** Extra functions, merged over the default, draft and `mf1:` functions. */
  functions?: Record<string, MessageFunction<string>>;
}

const builtins = { ...DraftFunctions, ...MF1Functions };

function formatter(message: Message, locale: string | string[], opts: FormatOptions) {
  const functions = opts.functions ? { ...builtins, ...opts.functions } : builtins;
  const mfOpts: ConstructorParameters<typeof MessageFormat>[2] = { functions };
  if (opts.bidiIsolation) mfOpts.bidiIsolation = opts.bidiIsolation;
  if (opts.dir) mfOpts.dir = opts.dir;
  return new MessageFormat(locale, toReference(message), mfOpts);
}

const reporter = (opts: FormatOptions) => (error: unknown) => {
  const e = error as Partial<FormatError> | null;
  const out: FormatError = {
    type: typeof e?.type === "string" ? e.type : "function-error",
    message: typeof e?.message === "string" ? e.message : String(error),
  };
  if (typeof e?.source === "string") out.source = e.source;
  opts.onError?.(out);
};

/** Format a canonical message with the reference formatter. */
export function format(
  message: Message,
  locale: string | string[],
  values: Record<string, unknown> = {},
  opts: FormatOptions = {},
): string {
  return formatter(message, locale, opts).format(values, reporter(opts));
}

/** Format a canonical message to parts with the reference formatter. */
export function formatToParts(
  message: Message,
  locale: string | string[],
  values: Record<string, unknown> = {},
  opts: FormatOptions = {},
): MessagePart<string>[] {
  return formatter(message, locale, opts).formatToParts(values, reporter(opts));
}
