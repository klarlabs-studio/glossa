/**
 * zod schemas for the responses Studio trusts: they are checked at the
 * boundary before any view sees them. The compile-time assertions at the
 * bottom tie each schema to the generated contract types, so a contract
 * change that breaks a schema fails `pnpm lint`.
 */
import { z } from "zod";
import type { components } from "./schema";

const timestamp = z.string().min(1);
const id = z.string().min(1);

export const Role = z.enum(["owner", "admin", "developer", "translator", "reviewer"]);
export const Syntax = z.enum(["mf1", "mf2"]);
export const Direction = z.enum(["ltr", "rtl"]);
export const ReviewState = z.enum(["draft", "needs_review", "approved", "rejected"]);
/** `ai`: the platform translating for a person. `agent`: an autonomous agent writing through MCP (RFC 0005 §7.3). */
export const Origin = z.enum(["human", "ai", "agent", "translation_memory", "machine_translation", "import", "adaptation"]);
/** `proposed`: new on an open branch (RFC 0004 §4.1); shown like `active`, with a pill. */
export const MessageState = z.enum(["active", "proposed", "obsolete"]);
export const Platform = z.enum(["web", "api", "ios", "android", "other"]);

export const FieldError = z.object({ pointer: z.string(), detail: z.string() });

export const QAFinding = z.object({
  code: z.string(),
  severity: z.enum(["error", "warning"]),
  subject: z.string().optional(),
  detail: z.string().optional(),
  message: z.string(),
});

export const Problem = z.object({
  type: z.string(),
  title: z.string(),
  status: z.number().int(),
  code: z.string(),
  detail: z.string().optional(),
  instance: z.string().optional(),
  errors: z.array(FieldError).optional(),
  findings: z.array(QAFinding).optional(),
});

export const Person = z.object({
  id,
  email: z.string(),
  display_name: z.string().optional(),
  email_verified: z.boolean(),
  totp_enabled: z.boolean(),
  individual_tenant_id: id,
  created_at: timestamp,
});

export const Session = z.object({ person: Person, csrf_token: z.string().min(1), expires_at: timestamp });

export const Tenant = z.object({
  id,
  kind: z.enum(["individual", "organization"]),
  slug: z.string(),
  name: z.string(),
  created_at: timestamp,
});

export const Membership = z.object({
  member_id: id,
  tenant: Tenant,
  roles: z.array(Role),
  locales: z.array(z.string()),
});

export const Me = z.object({ person: Person, csrf_token: z.string().min(1), memberships: z.array(Membership) });

export const Passkey = z.object({ id: z.string(), name: z.string(), created_at: timestamp, last_used_at: timestamp.optional() });

export const SignInMethod = z.enum(["passkey", "password", "magic_link"]);
/** What the deployment offers (GET /v1/meta). */
export const Meta = z.object({
  sign_in_methods: z.array(SignInMethod),
  email_delivery: z.boolean(),
  edge_url: z.string().optional(),
});
export const PasskeyOptions = z.object({ options: z.record(z.string(), z.unknown()) });
export const TotpEnrollment = z.object({ secret: z.string(), otpauth_uri: z.string() });

/**
 * The project's check policy: what `glossa check` and the Glossa
 * pull-request check decide by (RFC 0004 §6.4). `require_complete` says
 * which locales must be complete — every one, the ones in `locales`, or
 * none.
 */
export const CheckPolicy = z.object({
  require_complete: z.enum(["all", "listed", "none"]),
  locales: z.array(z.string()).optional(),
  fail_on: z.enum(["error", "warning", "never"]),
  missing_translations: z.enum(["error", "warning"]),
});
export const ProjectSettings = z.object({
  default_syntax: Syntax,
  review_required: z.boolean(),
  check_policy: CheckPolicy.optional(),
});
export const Project = z.object({
  id,
  slug: z.string(),
  name: z.string(),
  source_locale: z.string(),
  settings: ProjectSettings,
  created_at: timestamp,
  updated_at: timestamp,
});

export const Application = z.object({
  id,
  slug: z.string(),
  name: z.string(),
  platform: Platform,
  created_at: timestamp,
  updated_at: timestamp,
});

/** The MF2 data model; its structure is checked by the formatter itself. */
export const MF2Message = z.looseObject({ type: z.enum(["message", "select"]) });

export const Argument = z.object({
  name: z.string(),
  type: z.enum(["string", "number", "integer", "percent", "currency", "date", "time", "datetime", "unit", "select"]),
  function: z.string().optional(),
  selector: z
    .object({ kind: z.enum(["plural", "ordinal", "exact", "string"]), keys: z.array(z.string()) })
    .optional(),
});

export const MarkupElement = z.object({ name: z.string(), kind: z.enum(["open", "standalone", "close"]) });

export const MessageContent = z.object({
  text: z.string(),
  syntax: Syntax,
  model: MF2Message,
  arguments: z.array(Argument),
  markup: z.array(MarkupElement),
});

export const Message = z.object({
  id,
  key: z.string(),
  namespace: z.string(),
  description: z.string(),
  max_length: z.number().int().optional(),
  state: MessageState,
  source: MessageContent,
  source_revision: z.number().int(),
  created_at: timestamp,
  updated_at: timestamp,
});

export const SourceRevision = z.object({
  revision: z.number().int(),
  text: z.string(),
  syntax: Syntax,
  model: MF2Message,
  author: z.string(),
  created_at: timestamp,
});

export const ProjectLocale = z.object({
  code: z.string(),
  direction: Direction,
  is_source: z.boolean(),
  created_at: timestamp,
});

export const FallbackGraph = z.object({ fallback: z.record(z.string(), z.array(z.string())) });

export const Translation = z.object({
  id,
  message_id: id,
  locale: z.string(),
  text: z.string(),
  syntax: Syntax,
  model: MF2Message,
  state: ReviewState,
  origin: Origin,
  author: z.string(),
  review_by_author_allowed: z.boolean().optional(),
  source_revision: z.number().int(),
  current_source_revision: z.number().int(),
  outdated: z.boolean(),
  warnings: z.array(QAFinding),
  revision: z.number().int(),
  created_at: timestamp,
  updated_at: timestamp,
});

/** A translation with its message, from the project-wide listing. */
export const ProjectTranslation = Translation.extend({
  key: z.string(),
  namespace: z.string(),
  message_state: MessageState,
});

const ReviewStateCounts = z.object({
  draft: z.number().int().min(0),
  needs_review: z.number().int().min(0),
  approved: z.number().int().min(0),
  rejected: z.number().int().min(0),
});
export const LocaleStats = z.object({
  code: z.string(),
  direction: Direction,
  is_source: z.boolean(),
  translated: z.number().int().min(0),
  missing: z.number().int().min(0),
  outdated: z.number().int().min(0),
  states: ReviewStateCounts,
});
export const TranslationStats = z.object({ messages: z.number().int().min(0), locales: z.array(LocaleStats) });

export const MessagePreviewError = z.object({ stage: z.enum(["parse", "format"]), code: z.string(), message: z.string() });
export const MessagePreview = z.object({
  valid: z.boolean(),
  message: MF2Message.optional(),
  mf2: z.string().optional(),
  arguments: z.array(Argument),
  markup: z.array(MarkupElement),
  formatted: z.string().optional(),
  errors: z.array(MessagePreviewError),
});

export const TranslationRevision = z.object({
  revision: z.number().int(),
  kind: z.enum(["content", "review"]),
  text: z.string(),
  syntax: Syntax,
  state: ReviewState,
  origin: Origin,
  origin_detail: z.record(z.string(), z.unknown()),
  author: z.string(),
  self_review: z.boolean(),
  source_revision: z.number().int(),
  findings: z.array(QAFinding),
  created_at: timestamp,
});

const ItemError = z.object({ code: z.string(), detail: z.string(), findings: z.array(QAFinding).optional() });
export const MessageUpsertResult = z.object({
  results: z.array(
    z.object({
      key: z.string(),
      status: z.enum(["created", "revised", "updated", "unchanged", "failed"]),
      message: Message.optional(),
      error: ItemError.optional(),
    }),
  ),
});

export const Member = z.object({
  id,
  email: z.string(),
  person_id: id.optional(),
  display_name: z.string().optional(),
  status: z.enum(["invited", "active"]),
  roles: z.array(Role),
  locales: z.array(z.string()),
  projects: z.array(id),
  vendor_id: id.optional(),
  visibility: z.enum(["all", "assigned"]),
  created_at: timestamp,
  updated_at: timestamp,
});

// ── release ───────────────────────────────────────────────────────────
/** The review states an environment may ship; `rejected` never does. */
export const ShippableState = z.enum(["draft", "needs_review", "approved"]);

export const EnvironmentPolicy = z.object({
  states: z.array(ShippableState).min(1),
  include_outdated: z.boolean(),
});

/**
 * `standard` serves the main catalog; `branch` is one open branch's
 * preview, published again when the branch changes (RFC 0004 §4.2).
 */
export const EnvironmentKind = z.enum(["standard", "branch"]);

/** Exactly one of `member`, `role` or `group` — who may approve a release (RFC 0006 §5.1). Never a vendor. */
export const EnvironmentApprovalParty = z.object({
  member: z.string().min(1).optional(),
  role: Role.optional(),
  group: z.string().min(1).optional(),
});

/** `n` distinct people of `from`, never the requester (always true: no self-approval). */
export const EnvironmentApproval = z.object({
  n: z.number().int().min(1).max(10),
  from: EnvironmentApprovalParty,
  distinct_from_requester: z.boolean(),
});

export const Environment = z.object({
  name: z.string().min(1),
  kind: EnvironmentKind,
  branch: z.string().min(1).optional(),
  policy: EnvironmentPolicy,
  /** Present when a publish or promote here needs approval (RFC 0006 §5.1). */
  approval: EnvironmentApproval.optional(),
  current_release_id: id.optional(),
  created_at: timestamp,
  updated_at: timestamp,
});

export const DeploymentAction = z.enum(["publish", "promote", "rollback"]);
export const Deployment = z.object({
  number: z.number().int().min(1),
  release_id: id,
  previous_release_id: id.optional(),
  action: DeploymentAction,
  author: z.string(),
  created_at: timestamp,
  // The environment's completeness requirement was not met and somebody
  // went ahead anyway. The exception is the record: such a deployment
  // always carries its reason.
  forced: z.boolean(),
  force_reason: z.string().optional(),
});

export const ReleaseLocale = z.object({ code: z.string(), direction: Direction });
export const ReleaseLocaleCounts = z.object({ messages: z.number().int().min(0), outdated: z.number().int().min(0) });
export const ReleaseCounts = z.object({
  messages: z.number().int().min(0),
  artifacts: z.number().int().min(0),
  bytes: z.number().int().min(0),
  new_artifacts: z.number().int().min(0),
  locales: z.record(z.string(), ReleaseLocaleCounts),
});

export const Release = z.object({
  id,
  version: z.number().int().min(1),
  parent_id: id.optional(),
  environment: z.string().min(1),
  policy: EnvironmentPolicy,
  manifest_digest: z.string().regex(/^[0-9a-f]{64}$/),
  source_locale: z.string(),
  locales: z.array(ReleaseLocale),
  counts: ReleaseCounts,
  note: z.string().optional(),
  author: z.string(),
  created_at: timestamp,
});

export const LocaleDiff = z.object({
  locale: z.string(),
  added: z.array(z.string()),
  changed: z.array(z.string()),
  removed: z.array(z.string()),
});
export const ReleaseDiff = z.object({
  release_id: id,
  base_release_id: id.optional(),
  locales: z.array(LocaleDiff),
});

export const ReleaseProblem = z.object({
  code: z.enum(["not_releasable"]),
  detail: z.string(),
  key: z.string().optional(),
  locale: z.string().optional(),
});
/** What a publish would ship (POST …/environments/{env}/release-previews); nothing is stored. */
export const ReleasePreview = z.object({
  environment: z.string().min(1),
  policy: EnvironmentPolicy,
  base_release_id: id.optional(),
  releasable: z.boolean(),
  problems: z.array(ReleaseProblem),
  manifest_digest: z.string().regex(/^[0-9a-f]{64}$/).optional(),
  source_locale: z.string().optional(),
  locales: z.array(ReleaseLocale).optional(),
  counts: ReleaseCounts.optional(),
  changes: z.array(LocaleDiff).optional(),
});

export const SigningKey = z.object({
  key_id: z.string().min(1),
  algorithm: z.enum(["Ed25519"]),
  public_key: z.string().min(1),
  active: z.boolean(),
});
export const SigningKeys = z.object({ keys: z.array(SigningKey) });

/**
 * What a delivery key reads at the edge: the environments on its
 * allowlist and, with `branches`, every branch preview. Anything else
 * answers 404 (RFC 0004 §4.3).
 */
export const DeliveryKeyScope = z.object({
  environments: z.array(z.string().min(1)).max(50),
  branches: z.boolean(),
});

export const DeliveryKey = z.object({
  id,
  name: z.string(),
  key: z.string().regex(/^glossa_pk_[A-Za-z0-9_-]{32}$/),
  scope: DeliveryKeyScope,
  created_by: z.string(),
  created_at: timestamp,
  revoked_at: timestamp.optional(),
});

/** A page of a list operation. */
export const page = <T extends z.ZodType>(item: T) =>
  z.object({ items: z.array(item), next_page_token: z.string().optional() });

export type Role = z.infer<typeof Role>;
export type Syntax = z.infer<typeof Syntax>;
export type ReviewState = z.infer<typeof ReviewState>;
export type Problem = z.infer<typeof Problem>;
export type QAFinding = z.infer<typeof QAFinding>;
export type Person = z.infer<typeof Person>;
export type Session = z.infer<typeof Session>;
export type Tenant = z.infer<typeof Tenant>;
export type Membership = z.infer<typeof Membership>;
export type Me = z.infer<typeof Me>;
export type Passkey = z.infer<typeof Passkey>;
export type TotpEnrollment = z.infer<typeof TotpEnrollment>;
export type SignInMethod = z.infer<typeof SignInMethod>;
export type Meta = z.infer<typeof Meta>;
export type ProjectTranslation = z.infer<typeof ProjectTranslation>;
export type TranslationStats = z.infer<typeof TranslationStats>;
export type LocaleStats = z.infer<typeof LocaleStats>;
export type MessagePreview = z.infer<typeof MessagePreview>;
export type MessagePreviewError = z.infer<typeof MessagePreviewError>;
export type ReleasePreview = z.infer<typeof ReleasePreview>;
export type ReleaseProblem = z.infer<typeof ReleaseProblem>;
export type Project = z.infer<typeof Project>;
export type ProjectSettings = z.infer<typeof ProjectSettings>;
export type CheckPolicy = z.infer<typeof CheckPolicy>;
export type Application = z.infer<typeof Application>;
export type Argument = z.infer<typeof Argument>;
export type MarkupElement = z.infer<typeof MarkupElement>;
export type Message = z.infer<typeof Message>;
export type SourceRevision = z.infer<typeof SourceRevision>;
export type ProjectLocale = z.infer<typeof ProjectLocale>;
export type FallbackGraph = z.infer<typeof FallbackGraph>;
export type Translation = z.infer<typeof Translation>;
export type TranslationRevision = z.infer<typeof TranslationRevision>;
export type MessageUpsertResult = z.infer<typeof MessageUpsertResult>;
export type Platform = z.infer<typeof Platform>;
export type Member = z.infer<typeof Member>;
export type SigningKeys = z.infer<typeof SigningKeys>;
export type ShippableState = z.infer<typeof ShippableState>;
export type EnvironmentPolicy = z.infer<typeof EnvironmentPolicy>;
export type Environment = z.infer<typeof Environment>;
export type EnvironmentApproval = z.infer<typeof EnvironmentApproval>;
export type EnvironmentApprovalParty = z.infer<typeof EnvironmentApprovalParty>;
export type Deployment = z.infer<typeof Deployment>;
export type Release = z.infer<typeof Release>;
export type ReleaseCounts = z.infer<typeof ReleaseCounts>;
export type LocaleDiff = z.infer<typeof LocaleDiff>;
export type ReleaseDiff = z.infer<typeof ReleaseDiff>;
export type SigningKey = z.infer<typeof SigningKey>;
export type DeliveryKey = z.infer<typeof DeliveryKey>;
export type DeliveryKeyScope = z.infer<typeof DeliveryKeyScope>;
export type EnvironmentKind = z.infer<typeof EnvironmentKind>;

// ── contract alignment (compile time only) ─────────────────────────────
type S = components["schemas"];
type Fits<A, B> = [A] extends [B] ? true : false;
type Assert<T extends true> = T;
export type ContractAlignment = [
  Assert<Fits<Problem, S["Problem"]>>,
  Assert<Fits<Session, S["Session"]>>,
  Assert<Fits<Me, S["Me"]>>,
  Assert<Fits<Project, S["Project"]>>,
  Assert<Fits<CheckPolicy, S["CheckPolicy"]>>,
  Assert<Fits<Application, S["Application"]>>,
  Assert<Fits<Argument, S["Argument"]>>,
  Assert<Fits<Message, S["Message"]>>,
  Assert<Fits<SourceRevision, S["SourceRevision"]>>,
  Assert<Fits<ProjectLocale, S["ProjectLocale"]>>,
  Assert<Fits<FallbackGraph, S["FallbackGraph"]>>,
  Assert<Fits<Translation, S["Translation"]>>,
  Assert<Fits<TranslationRevision, S["TranslationRevision"]>>,
  Assert<Fits<MessageUpsertResult, S["MessageUpsertResult"]>>,
  Assert<Fits<Passkey, S["Passkey"]>>,
  Assert<Fits<TotpEnrollment, S["TotpEnrollment"]>>,
  Assert<Fits<Member, S["Member"]>>,
  Assert<Fits<Environment, S["Environment"]>>,
  Assert<Fits<Deployment, S["Deployment"]>>,
  Assert<Fits<Release, S["Release"]>>,
  Assert<Fits<ReleaseDiff, S["ReleaseDiff"]>>,
  Assert<Fits<SigningKeys, S["SigningKeys"]>>,
  Assert<Fits<DeliveryKey, S["DeliveryKey"]>>,
  Assert<Fits<DeliveryKeyScope, S["DeliveryKeyScope"]>>,
  Assert<Fits<Meta, S["Meta"]>>,
  Assert<Fits<ProjectTranslation, S["ProjectTranslation"]>>,
  Assert<Fits<TranslationStats, S["TranslationStats"]>>,
  Assert<Fits<MessagePreview, S["MessagePreview"]>>,
  Assert<Fits<ReleasePreview, S["ReleasePreview"]>>,
];
