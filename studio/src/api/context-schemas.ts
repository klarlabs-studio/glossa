/**
 * zod schemas for the Context context (RFC 0004 §2–§3): where a message
 * is used in the code, which screenshots show it, and which active
 * messages no current build uses. Kept out of ./schemas so only the
 * screens that ask for context load them; the compile-time assertions at
 * the bottom tie each to the contract.
 */
import { z } from "zod";
import type { components } from "./schema";

const timestamp = z.string().min(1);
const id = z.string().min(1);

export const ContextSource = z.enum(["plugin", "extract", "runtime", "capture"]);
export const UsageKind = z.enum(["t", "component", "element", "accessor", "template"]);

export const ContextUsage = z.object({
  key: z.string(),
  message_id: id.optional(),
  file: z.string(),
  line: z.number().int(),
  column: z.number().int().optional(),
  component: z.string().optional(),
  route: z.string().optional(),
  kind: UsageKind,
  build_id: id,
  application_id: id,
  commit: z.string(),
  branch: z.string(),
  on_default_branch: z.boolean(),
  source: ContextSource,
});

export const ContextUsageList = z.object({ items: z.array(ContextUsage), next_page_token: z.string().optional() });

export const MessageUsages = z.object({
  message_id: id,
  key: z.string(),
  usages: z.array(ContextUsage),
  truncated: z.boolean(),
});

export const UnusedMessage = z.object({ id, key: z.string() });

export const UnusedMessageList = z.object({
  items: z.array(UnusedMessage),
  next_page_token: z.string().optional(),
  /** Builds considered; 0 means nothing was uploaded yet, so nothing is known. */
  current_builds: z.number().int().min(0),
  active_messages: z.number().int().min(0),
  unused_messages: z.number().int().min(0),
});

export const CaptureViewport = z.object({ width: z.number().int(), height: z.number().int() });

export const CaptureImage = z.object({
  digest: z.string(),
  width: z.number().int(),
  height: z.number().int(),
  /**
   * The image's own API path. Studio and /v1 share an origin, so the
   * session cookie travels with an `<img>` request; anything but a path
   * on this API would be a contract break, and is refused here.
   */
  url: z.string().startsWith("/v1/"),
});

export const CaptureBox = z.object({
  x: z.number().int(),
  y: z.number().int(),
  width: z.number().int().min(0),
  height: z.number().int().min(0),
});

export const CaptureRegionKind = z.enum(["element", "text", "attribute"]);

export const CaptureRegion = z.object({
  kind: CaptureRegionKind,
  box: CaptureBox,
  /** false when the message rendered zero-size or off-screen. */
  visible: z.boolean(),
});

export const MessageCapture = z.object({
  id,
  build_id: id,
  application_id: id,
  commit: z.string(),
  branch: z.string(),
  on_default_branch: z.boolean(),
  route: z.string(),
  viewport: CaptureViewport,
  locale: z.string(),
  image: CaptureImage,
  regions: z.array(CaptureRegion),
  created_at: timestamp,
});

export const MessageCaptures = z.object({
  message_id: id,
  key: z.string(),
  captures: z.array(MessageCapture),
  truncated: z.boolean(),
});

export type ContextSource = z.infer<typeof ContextSource>;
export type UsageKind = z.infer<typeof UsageKind>;
export type ContextUsage = z.infer<typeof ContextUsage>;
export type ContextUsageList = z.infer<typeof ContextUsageList>;
export type MessageUsages = z.infer<typeof MessageUsages>;
export type UnusedMessage = z.infer<typeof UnusedMessage>;
export type UnusedMessageList = z.infer<typeof UnusedMessageList>;
export type CaptureViewport = z.infer<typeof CaptureViewport>;
export type CaptureImage = z.infer<typeof CaptureImage>;
export type CaptureBox = z.infer<typeof CaptureBox>;
export type CaptureRegionKind = z.infer<typeof CaptureRegionKind>;
export type CaptureRegion = z.infer<typeof CaptureRegion>;
export type MessageCapture = z.infer<typeof MessageCapture>;
export type MessageCaptures = z.infer<typeof MessageCaptures>;

// ── contract alignment (compile time only) ─────────────────────────────
type C = components["schemas"];
type Fits<A, B> = [A] extends [B] ? true : false;
type Assert<T extends true> = T;
export type ContextContractAlignment = [
  Assert<Fits<ContextSource, C["ContextSource"]>>,
  Assert<Fits<UsageKind, C["UsageKind"]>>,
  Assert<Fits<ContextUsage, C["ContextUsage"]>>,
  Assert<Fits<ContextUsageList, C["ContextUsageList"]>>,
  Assert<Fits<MessageUsages, C["MessageUsages"]>>,
  Assert<Fits<UnusedMessage, C["UnusedMessage"]>>,
  Assert<Fits<UnusedMessageList, C["UnusedMessageList"]>>,
  Assert<Fits<CaptureViewport, C["CaptureViewport"]>>,
  Assert<Fits<CaptureImage, C["CaptureImage"]>>,
  Assert<Fits<CaptureBox, C["CaptureBox"]>>,
  Assert<Fits<CaptureRegion, C["CaptureRegion"]>>,
  Assert<Fits<MessageCapture, C["MessageCapture"]>>,
  Assert<Fits<MessageCaptures, C["MessageCaptures"]>>,
];
