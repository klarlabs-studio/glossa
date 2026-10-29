/**
 * `<glossa-overlay>`: the in-product editor's panel (RFC 0004 §5.3). It
 * shows a message's source and its translation in the session's locale,
 * validates edits through the API's MessageFormat kernel, saves them as
 * ordinary translation revisions (provenance `human`, `origin_detail.in_context`,
 * `If-Match` concurrency), previews them live through the runtime's
 * `override`, and offers history, terminology findings, AI suggestions and a
 * link to Studio.
 *
 * It is a modal dialog in a shadow root: focus is trapped while it's open,
 * Esc closes it and returns focus, and every control is labelled. Comments
 * aren't shown: the API has no comments yet.
 */
import { LitElement, html, nothing } from "lit";
import type { PropertyDeclarations, TemplateResult } from "lit";
import type { Message as Model } from "@klarlabs-studio/glossa-runtime";

import { ApiError } from "./api.js";
import type {
  AIFillPreview,
  AISuggestion,
  InContext,
  Message,
  MessagePreview,
  OverlayApi,
  QAFinding,
  Syntax,
  TermFinding,
  Translation,
  TranslationRevision,
} from "./api.js";
import { styles } from "./styles.js";
import type { Target } from "./target.js";

/** What the panel needs from the session that created it. */
export interface OverlayHost {
  api: OverlayApi;
  /** The locale being edited. */
  locale: string;
  /** Studio's page for this message. */
  studioUrl(key: string): string;
  /** The route pattern and viewport an edit is made in. */
  inContext(): InContext;
  /** Render `model` for `id` in the page (the runtime's override); no model clears it. */
  preview(id: string, model?: Model): void;
  /** Resolves after `ms`; the AI poll's clock. */
  wait(ms: number): Promise<void>;
}

type Notice = { kind: "error" | "ok" | "conflict"; text: string };

interface AIState {
  phase: "checking" | "preview" | "waiting" | "ready" | "none" | "error";
  preview?: AIFillPreview;
  suggestion?: AISuggestion;
  note?: string;
}

/** How long the editor waits after typing before asking the server to parse. */
export const VALIDATE_DELAY = 300;
/** How often, and how patiently, the panel looks for a requested suggestion. */
export /** What a job that ended without a suggestion means, in the panel. */
const JOB_ENDINGS: Record<string, string> = {
  skipped: "Nothing to translate: the message changed, or it is in a namespace AI never sees.",
  failed: "The provider didn't answer. Try again in a moment.",
  dead: "The provider kept failing. Look in Studio for what went wrong.",
  cancelled: "The job was cancelled.",
};

const AI_POLL = [1000, 2000, 3000, 5000, 5000, 5000];

const FOCUSABLE =
  'a[href], button:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])';

/** A problem, in words for the person editing. */
function describe(e: unknown): string {
  if (!(e instanceof ApiError))
    return "Glossa can't be reached. Check your connection and try again.";
  if (e.status === 401) return "Your in-context session has expired. Sign in again from Studio.";
  if (e.status === 403) return "You don't have permission to do this in this project or locale.";
  if (e.status === 404) return "This message isn't in the project's catalog.";
  if (e.status === 429) return "Too many requests. Wait a moment and try again.";
  const fields = e.problem?.errors?.map((f) => f.detail).join(" ");
  return [e.message, fields].filter(Boolean).join(" ");
}

const date = (s: string) => {
  const d = new Date(s);
  return Number.isNaN(d.valueOf()) ? s : d.toISOString().slice(0, 16).replace("T", " ");
};

const state = (s: string) => s.replace(/_/g, " ");

export class GlossaOverlay extends LitElement {
  static override styles = styles;

  static override properties: PropertyDeclarations = {
    target: { state: true },
    message: { state: true },
    translation: { state: true },
    draft: { state: true },
    syntax: { state: true },
    check: { state: true },
    busy: { state: true },
    notice: { state: true },
    findings: { state: true },
    history: { state: true },
    terms: { state: true },
    ai: { state: true },
    expanded: { state: true },
    fromSuggestion: { state: true },
  };

  /** Set by `activate()` before the panel opens. */
  host?: OverlayHost;

  protected target?: Target;
  protected message?: Message;
  protected translation?: Translation | null;
  protected draft = "";
  protected syntax: Syntax = "mf2";
  protected check?: MessagePreview;
  protected busy: "" | "loading" | "saving" = "";
  protected notice?: Notice;
  protected findings: QAFinding[] = [];
  protected history?: TranslationRevision[];
  protected terms?: TermFinding[];
  protected ai?: AIState;
  protected expanded: ReadonlySet<string> = new Set();
  protected fromSuggestion?: AISuggestion;

  private etag?: string;
  /** The models saved in this session, by message: closing keeps them previewed. */
  private readonly saved = new Map<string, Model>();
  private returnFocus?: HTMLElement;
  private timer?: ReturnType<typeof setTimeout>;
  /** Bumped on every open and close, so late answers for another message are dropped. */
  private generation = 0;
  /** Bumped on every edit, so a late parse of an older draft is dropped. */
  private checks = 0;

  /** Whether the panel is showing a message. */
  get isOpen(): boolean {
    return !!this.target;
  }

  /** Open the panel on a rendered message (or switch to another one). */
  async open(target: Pick<Target, "id"> & Partial<Target>): Promise<void> {
    const host = this.host;
    if (!host) throw new Error("glossa-overlay: activate() sets the host before open()");
    if (this.target) this.release();
    if (!this.returnFocus) this.returnFocus = deepActive(this.ownerDocument);
    const gen = ++this.generation;
    this.target = { kind: "element", element: this, ...target } as Target;
    Object.assign(this, {
      message: undefined,
      translation: undefined,
      check: undefined,
      notice: undefined,
      findings: [],
      history: undefined,
      terms: undefined,
      ai: undefined,
      expanded: new Set(),
      fromSuggestion: undefined,
      etag: undefined,
      draft: "",
      busy: "loading",
    });
    await this.updateComplete;
    this.focusFirst();
    try {
      const [m, t] = await Promise.all([
        host.api.getMessage(target.id),
        host.api.getTranslation(target.id, host.locale),
      ]);
      if (gen !== this.generation) return;
      this.message = m.value;
      this.translation = t?.value ?? null;
      this.etag = t?.etag;
      this.draft = t?.value.text ?? "";
      this.syntax = t?.value.syntax ?? m.value.source.syntax;
      this.findings = t?.value.warnings ?? [];
    } catch (e) {
      if (gen === this.generation) this.notice = { kind: "error", text: describe(e) };
    } finally {
      if (gen === this.generation) this.busy = "";
    }
    await this.updateComplete;
    this.shadowRoot?.querySelector<HTMLTextAreaElement>("#draft")?.focus();
  }

  /** Close the panel. The page keeps showing what was saved, and drops unsaved previews. */
  close(): void {
    if (!this.target) return;
    this.release();
    this.target = undefined;
    const back = this.returnFocus;
    this.returnFocus = undefined;
    if (back?.isConnected) back.focus();
  }

  /** Put the page back to what's saved: the session's saved model, or the release's. */
  private release(): void {
    clearTimeout(this.timer);
    this.generation++;
    if (this.target && this.host) this.host.preview(this.target.id, this.saved.get(this.target.id));
  }

  override disconnectedCallback(): void {
    clearTimeout(this.timer);
    super.disconnectedCallback();
  }

  // ── editing ────────────────────────────────────────────────────────

  private onInput(e: Event): void {
    this.draft = (e.target as HTMLTextAreaElement).value;
    this.scheduleCheck();
  }

  private onSyntax(e: Event): void {
    this.syntax = (e.target as HTMLSelectElement).value as Syntax;
    this.scheduleCheck();
  }

  private scheduleCheck(): void {
    clearTimeout(this.timer);
    this.check = undefined;
    const gen = this.generation;
    const seq = ++this.checks;
    const current = () => gen === this.generation && seq === this.checks;
    this.timer = setTimeout(() => void this.validate(current), VALIDATE_DELAY);
  }

  /** Parse the draft on the server; a valid one previews live in the page. */
  private async validate(current: () => boolean): Promise<void> {
    const { host, target } = this;
    if (!host || !target || !this.draft.trim()) return;
    try {
      const check = await host.api.previewMessage(this.draft, this.syntax, host.locale);
      if (!current()) return;
      this.check = check;
      if (check.valid && check.message) host.preview(target.id, check.message);
    } catch (e) {
      if (current()) this.notice = { kind: "error", text: describe(e) };
    }
  }

  private get invalid(): boolean {
    return this.check?.valid === false;
  }

  private async save(e?: Event): Promise<void> {
    e?.preventDefault();
    const { host, target, message } = this;
    if (!host || !target || !message || this.busy || this.invalid || !this.draft.trim()) return;
    clearTimeout(this.timer);
    this.checks++;
    this.busy = "saving";
    this.notice = undefined;
    const gen = this.generation;
    try {
      let saved: Translation;
      const suggestion = this.fromSuggestion;
      if (suggestion) {
        const edited = this.draft !== suggestion.message;
        await host.api.accept(
          suggestion.id,
          edited ? { text: this.draft, syntax: this.syntax } : undefined,
          host.inContext(),
        );
        const t = await host.api.getTranslation(message.key, host.locale);
        if (!t) throw new Error("accepted suggestion wrote no translation");
        saved = t.value;
        this.etag = t.etag;
        this.fromSuggestion = undefined;
      } else {
        const r = await host.api.putTranslation(
          message.key,
          host.locale,
          { text: this.draft, syntax: this.syntax, inContext: host.inContext() },
          this.translation ? this.etag : undefined,
        );
        saved = r.value;
        this.etag = r.etag;
      }
      if (gen !== this.generation) return;
      this.translation = saved;
      this.saved.set(target.id, saved.model);
      this.findings = saved.warnings;
      this.history = undefined;
      if (this.expanded.has("history")) void this.loadHistory();
      host.preview(target.id, saved.model);
      this.notice = {
        kind: "ok",
        text: `Saved as revision ${saved.revision} (${state(saved.state)}).`,
      };
    } catch (e) {
      if (gen === this.generation) this.failed(e);
    } finally {
      if (gen === this.generation) this.busy = "";
    }
  }

  private failed(e: unknown): void {
    if (e instanceof ApiError && (e.status === 412 || e.code === "translation_conflict")) {
      this.notice = {
        kind: "conflict",
        text: "Someone changed this translation since you opened it. Your text is kept: load the latest version to compare, then save again.",
      };
    } else if (e instanceof ApiError && e.code === "structural_qa_failed") {
      this.findings = e.problem?.findings ?? [];
      this.notice = {
        kind: "error",
        text: "The translation doesn't fit its source. Fix the errors below.",
      };
    } else {
      this.notice = { kind: "error", text: describe(e) };
    }
  }

  /** After a conflict: take the latest translation and its ETag, keep the draft. */
  private async loadLatest(): Promise<void> {
    const { host, message } = this;
    if (!host || !message) return;
    try {
      const t = await host.api.getTranslation(message.key, host.locale);
      this.translation = t?.value ?? null;
      this.etag = t?.etag;
      this.notice = {
        kind: "ok",
        text: t
          ? `Loaded revision ${t.value.revision}. Save again to replace it with your text.`
          : "The translation was removed.",
      };
      this.history = undefined;
      if (this.expanded.has("history")) void this.loadHistory();
    } catch (e) {
      this.notice = { kind: "error", text: describe(e) };
    }
  }

  // ── history, terms, AI ─────────────────────────────────────────────

  private toggle(section: "history" | "terms" | "ai"): void {
    const next = new Set(this.expanded);
    if (next.has(section)) next.delete(section);
    else {
      next.add(section);
      if (section === "history" && !this.history) void this.loadHistory();
      if (section === "terms" && !this.terms) void this.loadTerms();
      if (section === "ai" && !this.ai) void this.askAI();
    }
    this.expanded = next;
  }

  private async loadHistory(): Promise<void> {
    const { host, message } = this;
    if (!host || !message) return;
    try {
      this.history = this.translation ? await host.api.revisions(message.key, host.locale) : [];
    } catch (e) {
      this.history = [];
      this.notice = { kind: "error", text: describe(e) };
    }
  }

  private async loadTerms(): Promise<void> {
    const { host, message } = this;
    if (!host || !message) return;
    try {
      this.terms = await host.api.termFindings(message.key, host.locale);
    } catch (e) {
      this.terms = [];
      this.notice = { kind: "error", text: describe(e) };
    }
  }

  /** A pending suggestion if there is one, else what a fill would do. */
  private async askAI(): Promise<void> {
    const { host, message } = this;
    if (!host || !message) return;
    this.ai = { phase: "checking" };
    try {
      const [pending] = await host.api.suggestions(message.id, host.locale);
      if (pending) {
        this.ai = { phase: "ready", suggestion: pending };
        return;
      }
      const preview = await host.api.previewFill(message.key, host.locale);
      const l = preview.locales[0];
      const runs = l ? l.provider + l.tm_exact + l.existing : 0;
      if (runs > 0) this.ai = { phase: "preview", preview };
      else this.ai = { phase: "none", preview, note: whyNot(preview) };
    } catch (e) {
      this.ai = { phase: "error", note: describe(e) };
    }
  }

  /** Queue the fill for this one message, then look for its suggestion for a while. */
  private async requestAI(): Promise<void> {
    const { host, message } = this;
    if (!host || !message) return;
    this.ai = { ...this.ai, phase: "waiting" };
    try {
      const fill = await host.api.fill(message.key, host.locale);
      const [job] = fill.job_ids ?? [];
      for (const ms of AI_POLL) {
        await host.wait(ms);
        if (this.message !== message) return;
        const [s] = await host.api.suggestions(message.id, host.locale);
        if (s) {
          this.ai = { phase: "ready", suggestion: s };
          return;
        }
        // The fill names its job, so a job that ended without a
        // suggestion says why now instead of after every poll.
        const ended = job ? await this.jobEnded(job) : undefined;
        if (ended) {
          this.ai = { phase: "none", note: ended };
          return;
        }
      }
      this.ai = {
        phase: "none",
        note: "No suggestion yet. It will appear in Studio's review queue when the job finishes.",
      };
    } catch (e) {
      this.ai = { phase: "error", note: describe(e) };
    }
  }

  /**
   * Why a fill's job produced nothing, or undefined while it is still
   * running. An old server that names no job is simply not asked.
   */
  private async jobEnded(id: string): Promise<string | undefined> {
    let job;
    try {
      job = await this.host?.api.job(id);
    } catch {
      return undefined; // following the job is a nicety, not the answer
    }
    if (!job || job.state === "queued" || job.state === "running") return undefined;
    if (job.state === "succeeded") return undefined; // the suggestion is on its way
    return JOB_ENDINGS[job.state] ?? "The job didn't finish. Try again, or look in Studio.";
  }

  /** Put the suggestion into the editor; saving then accepts it (with the edits). */
  private useSuggestion(s: AISuggestion): void {
    this.fromSuggestion = s;
    this.draft = s.message;
    this.syntax = "mf2";
    if (this.host && this.target) this.host.preview(this.target.id, s.model);
    this.scheduleCheck();
    void this.updateComplete.then(() =>
      this.shadowRoot?.querySelector<HTMLTextAreaElement>("#draft")?.focus(),
    );
  }

  // ── keyboard ───────────────────────────────────────────────────────

  private onKeydown(e: KeyboardEvent): void {
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      this.close();
      return;
    }
    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
      void this.save(e);
      return;
    }
    if (e.key !== "Tab") return;
    const all = this.focusables();
    if (!all.length) return;
    const active = this.shadowRoot?.activeElement;
    const first = all[0]!;
    const last = all[all.length - 1]!;
    if (e.shiftKey && (active === first || !active)) {
      e.preventDefault();
      last.focus();
    } else if (!e.shiftKey && (active === last || !active)) {
      e.preventDefault();
      first.focus();
    }
  }

  private focusables(): HTMLElement[] {
    return Array.from(this.shadowRoot?.querySelectorAll<HTMLElement>(FOCUSABLE) ?? []);
  }

  private focusFirst(): void {
    this.focusables()[0]?.focus();
  }

  // ── rendering ──────────────────────────────────────────────────────

  protected override render(): TemplateResult | typeof nothing {
    const { target, host } = this;
    if (!target || !host) return nothing;
    const fallback = target.locale && target.locale !== host.locale;
    const where = fallback
      ? `In ${host.locale}. The page shows the ${target.locale} fallback until this locale has a translation.`
      : `In ${host.locale}.`;
    return html`<div
      role="dialog"
      aria-modal="true"
      aria-labelledby="title"
      aria-describedby="where"
      aria-busy=${this.busy === "loading" ? "true" : "false"}
      @keydown=${this.onKeydown}
    >
      <header>
        <div>
          <h2 id="title">Edit <code>${target.id}</code></h2>
          <p id="where" class="meta">${where}</p>
        </div>
        <button class="close" type="button" aria-label="Close editor" @click=${() => this.close()}>
          ×
        </button>
      </header>
      ${this.busy === "loading" ? html`<p role="status">Loading…</p>` : this.body()}
    </div>`;
  }

  private body() {
    const { message, host } = this;
    if (!message || !host) return this.renderNotice();
    const t = this.translation;
    return html`
      <h3>Source</h3>
      <span class="text" dir="auto">${message.source.text}</span>
      ${message.description ? html`<p class="meta">${message.description}</p>` : nothing}
      ${message.max_length
        ? html`<p class="meta">At most ${message.max_length} characters.</p>`
        : nothing}

      <h3>Translation</h3>
      ${t
        ? html`<p class="meta">
            Revision ${t.revision} · ${state(t.state)} · ${t.origin} · ${t.author}
            ${t.outdated ? html`<span class="badge warn">outdated</span>` : nothing}
          </p>`
        : html`<p class="meta">Not translated yet.</p>`}

      <form @submit=${(e: Event) => this.save(e)}>
        <label for="draft">Translation (${host.locale})</label>
        <textarea
          id="draft"
          lang=${host.locale}
          dir="auto"
          spellcheck="true"
          aria-describedby="check"
          aria-invalid=${this.invalid ? "true" : "false"}
          .value=${this.draft}
          @input=${this.onInput}
        ></textarea>
        <label for="syntax">Syntax</label>
        <select id="syntax" .value=${this.syntax} @change=${this.onSyntax}>
          <option value="mf2">MessageFormat 2</option>
          <option value="mf1">ICU MessageFormat 1</option>
        </select>
        <div id="check" role="status" aria-live="polite">${this.renderCheck()}</div>
        ${this.fromSuggestion
          ? html`<p class="meta">Saving accepts the AI suggestion, with your edits.</p>`
          : nothing}
        <div class="row">
          <button
            class="primary"
            type="submit"
            ?disabled=${!!this.busy || this.invalid || !this.draft.trim()}
          >
            ${this.busy === "saving" ? "Saving…" : "Save"}
          </button>
          <a
            class="button"
            href=${host.studioUrl(message.key)}
            target="_blank"
            rel="noopener noreferrer"
            >Open in Studio<span class="visually-hidden"> (opens in a new tab)</span></a
          >
        </div>
      </form>
      ${this.renderNotice()} ${this.renderFindings()}
      ${this.section("history", "History", () => this.renderHistory())}
      ${this.section("terms", "Terminology", () => this.renderTerms())}
      ${this.section("ai", "Ask AI", () => this.renderAI())}
    `;
  }

  private renderCheck() {
    const c = this.check;
    if (!c) return nothing;
    if (c.valid) return html`<p class="ok">Valid message. Previewing it in the page.</p>`;
    return html`<ul class="error">
      ${c.errors.map((e) => html`<li><code>${e.code}</code> ${e.message}</li>`)}
    </ul>`;
  }

  private renderNotice() {
    const n = this.notice;
    if (!n) return nothing;
    const cls = n.kind === "ok" ? "ok" : "error";
    return html`<div class="notice ${cls}" role=${n.kind === "ok" ? "status" : "alert"}>
      <p>${n.text}</p>
      ${n.kind === "conflict"
        ? html`<button type="button" @click=${() => this.loadLatest()}>Load latest version</button>`
        : nothing}
    </div>`;
  }

  private renderFindings() {
    if (!this.findings.length) return nothing;
    return html`<h3>Checks</h3>
      <ul>
        ${this.findings.map(
          (f) =>
            html`<li class=${f.severity === "error" ? "error" : "warn"}>
              <code>${f.code}</code> ${f.message}
            </li>`,
        )}
      </ul>`;
  }

  private section(id: "history" | "terms" | "ai", label: string, content: () => unknown) {
    const open = this.expanded.has(id);
    return html`<button
        class="disclosure"
        type="button"
        aria-expanded=${open ? "true" : "false"}
        aria-controls="section-${id}"
        @click=${() => this.toggle(id)}
      >
        <span>${label}</span><span aria-hidden="true">${open ? "−" : "+"}</span>
      </button>
      <div id="section-${id}" ?hidden=${!open}>${open ? content() : nothing}</div>`;
  }

  private renderHistory() {
    const h = this.history;
    if (!h) return html`<p role="status">Loading history…</p>`;
    if (!h.length) return html`<p class="meta">No revisions yet.</p>`;
    return html`<ol reversed>
      ${h.map((r) => {
        const inContext = r.origin_detail?.in_context as InContext | undefined;
        return html`<li>
          <p class="meta">
            ${r.revision} · ${date(r.created_at)} · ${r.author} ·
            ${r.kind === "review"
              ? html`review: ${state(r.state)}`
              : html`${r.origin}, ${state(r.state)}`}
            ${inContext ? html` · edited in context on <code>${inContext.route}</code>` : nothing}
          </p>
          ${r.kind === "content" ? html`<span class="text" dir="auto">${r.text}</span>` : nothing}
        </li>`;
      })}
    </ol>`;
  }

  private renderTerms() {
    const t = this.terms;
    if (!t) return html`<p role="status">Checking terminology…</p>`;
    if (!t.length) return html`<p class="meta">No terminology findings.</p>`;
    return html`<ul>
      ${t.map(
        (f) =>
          html`<li class=${f.severity === "error" ? "error" : "warn"}>
            ${f.message}
            ${f.suggestions.length
              ? html`<span class="meta"> Use: ${f.suggestions.join(", ")}</span>`
              : nothing}
          </li>`,
      )}
    </ul>`;
  }

  private renderAI() {
    const ai = this.ai;
    if (!ai || ai.phase === "checking") return html`<p role="status">Looking for suggestions…</p>`;
    if (ai.phase === "waiting") return html`<p role="status">Waiting for the suggestion…</p>`;
    if (ai.phase === "none" || ai.phase === "error")
      return html`<p class=${ai.phase === "error" ? "error" : "meta"} role="status">${ai.note}</p>`;
    if (ai.phase === "preview") {
      const l = ai.preview?.locales[0];
      return html`<p class="meta">
          ${l?.tm_exact ? "An exact translation-memory match covers this message." : nothing}
          ${l?.existing ? "A job for this message exists already." : nothing}
          ${l?.provider ? "This asks the project's AI provider for a translation." : nothing}
        </p>
        ${ai.preview?.warnings.length
          ? html`<p class="warn">${ai.preview.warnings.map(warning).join(" ")}</p>`
          : nothing}
        <button type="button" @click=${() => this.requestAI()}>Request a suggestion</button>`;
    }
    const s = ai.suggestion!;
    return html`<span class="text" dir="auto" lang=${s.locale}>${s.message}</span>
      <p class="meta">Confidence ${Math.round(s.score * 100)}% · ${state(s.action)}</p>
      ${[...s.findings, ...s.term_findings].length
        ? html`<ul>
            ${[...s.findings, ...s.term_findings].map(
              (f) => html`<li class="warn">${f.message}</li>`,
            )}
          </ul>`
        : nothing}
      <div class="row">
        <button type="button" @click=${() => this.useSuggestion(s)}>Use in editor</button>
      </div>`;
  }
}

const WARNINGS: Record<string, string> = {
  provider_consent_off:
    "Sending text to AI providers is off for this tenant; only exact translation-memory matches are reused.",
  no_budget: "The AI budget for this month is used up.",
  no_provider: "No AI provider is configured.",
};
const warning = (w: string) => WARNINGS[w] ?? w;

/** Why a fill wouldn't produce a suggestion for this message. */
function whyNot(p: AIFillPreview): string {
  const l = p.locales[0];
  // up_to_date is no longer a reason here: the editor forces, precisely
  // because the text on screen is current (RFC 0004 §5.3). A server that
  // still reports it is one that doesn't know `force`.
  if (l?.skipped.up_to_date)
    return "This server's AI fills only missing or outdated translations.";
  const refused = Object.keys(l?.refused ?? {});
  if (refused.includes("sensitive"))
    return "This message is in a sensitive namespace and is never sent to AI.";
  if (p.warnings.length) return p.warnings.map(warning).join(" ");
  if (refused.length) return `AI can't translate this message (${refused.join(", ")}).`;
  return "AI has nothing to fill for this message.";
}

/** The focused element, through open shadow roots. */
export function deepActive(doc: Document): HTMLElement | undefined {
  let el = doc.activeElement;
  while (el?.shadowRoot?.activeElement) el = el.shadowRoot.activeElement;
  return el instanceof HTMLElement ? el : undefined;
}

if (!customElements.get("glossa-overlay")) customElements.define("glossa-overlay", GlossaOverlay);

declare global {
  interface HTMLElementTagNameMap {
    "glossa-overlay": GlossaOverlay;
  }
}
