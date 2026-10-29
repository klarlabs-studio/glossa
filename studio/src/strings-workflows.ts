/**
 * Studio's copy for one M5 surface (RFC 0006), kept beside strings.ts and
 * spread into it: `strings.workflows` and `strings.problems`. Plain data —
 * it imports nothing, so strings.ts stays the one place every screen reads.
 *
 * This one is the workflow editor (§2.3, §14 decision 12) and a
 * project's bindings.
 */
const subjects: Record<string, string> = { translation: "Translations", release_request: "Release requests" };

export const workflowStrings = {
  subject: subjects,
  // ── the list ──────────────────────────────────────────────────────
  title: "Workflows",
  lead: "How work moves from written to shippable in this workspace: who translates, who approves and in what order. Each workflow is a document — a state chart and the guards and actions it uses, drawn from a fixed vocabulary — and saving one never changes the four review states that decide what ships.",
  back: "Projects",
  loading: "Loading workflows…",
  failed: "The workflows could not be read, so nothing below is known — not even whether there are any.",
  empty: "No workflows yet. Until one is bound to a project, its translations are reviewed as before: no instances, no assignments.",
  readOnly: "You can read workflows here. Writing them needs the owner or admin role.",
  create: "New workflow",
  columns: { name: "Workflow", subject: "Runs on", version: "Version", scope: "Bindable by", saved: "Created" },
  everyProject: "Every project",
  onlyProject: (name: string) => `Only ${name}`,
  version: (n: number) => `v${n}`,
  remove: "Delete",
  removeOf: (name: string) => ` ${name}`,
  removeTitle: (name: string) => `Delete the workflow ${name}?`,
  removeLead:
    "Its bindings go with it, so no new work starts under it. Its versions stay readable, so whatever already ran on them stays explainable, and work in flight finishes on the version it started with.",
  removeDefault:
    "This is the default review workflow. Deleting it is allowed and means “no workflow” wherever it was bound: translations are reviewed as before M5. A release request still runs on a release-approval workflow, which Glossa seeds again when needed.",
  removeConfirm: "Delete workflow",
  removed: (name: string) => `Deleted the workflow ${name}. Its bindings were removed.`,
  retry: "Read again",

  // ── the editor ────────────────────────────────────────────────────
  newTitle: "New workflow",
  editTitle: (name: string) => `Workflow ${name}`,
  editorLead:
    "Edit the document; the chart beside it follows as you type. Glossa checks it with the same compile and lint a save runs, and nothing is saved that would strand a translation or use a guard or action it doesn't have.",
  allWorkflows: "All workflows",
  name: "Name",
  nameHint: "Lowercase letters, digits and dashes. The name is the workflow's identity: a new name is a new workflow.",
  subjectLabel: "Runs on",
  subjectHint: "What its instances are about. It can't change after the first save.",
  scope: "Bindable by",
  scopeHint: "Which projects may bind it. A workflow for one project stays out of the others' lists.",
  start: "Start writing",
  documentLabel: "Workflow document (JSON)",
  documentHint: "A glossa.workflow/v1 document: schema, name, subject, chart, guards and actions.",
  lines: (n: number) => `${n.toLocaleString()} line${n === 1 ? "" : "s"}`,
  unsaved: "Unsaved changes",
  saved: "Saved",
  check: "Check",
  checking: "Checking…",
  save: "Save",
  saveFirst: "Save version 1",
  saveNext: (n: number) => `Save version ${n}`,
  saving: "Saving…",
  readOnlyEditor: "You can read this workflow. Saving a new version needs the owner or admin role.",
  viewingVersion: (n: number, latest: number) => `You are reading version ${n}; the latest is version ${latest}.`,
  backToLatest: "Back to the latest version",
  restore: (n: number) => `Edit from version ${n}`,
  restoreHint: "Copies this version into the editor. Saving it makes a new version; no version is ever changed.",
  restored: (n: number) => `Version ${n} is in the editor. Save it to make it the latest.`,
  notJson: (line: number | undefined) => (line ? `This isn't valid JSON (around line ${line}).` : "This isn't valid JSON."),
  notObject: "A workflow document is a JSON object: it starts with { and ends with }.",
  renamed: (from: string, to: string) => `The name changed from ${from} to ${to}. A save keeps its workflow's name: rename by creating a new workflow.`,
  createdStatus: (name: string) => `Saved ${name} as version 1.`,
  savedStatus: (name: string, n: number) => `Saved ${name} as version ${n}. Running work stays on the version it started with.`,
  overtaken:
    "Someone saved a newer version while you were editing, so this save was refused rather than replace their change. Your text is still in the editor.",
  loadTheirs: "Load the latest version (discards your edits)",
  keepMine: "Keep my text and save over it",
  rejected: (n: number) => `Not saved: ${n.toLocaleString()} finding${n === 1 ? "" : "s"} to fix first. Nothing was stored.`,
  loadFailed: "This workflow could not be read.",

  // ── findings ──────────────────────────────────────────────────────
  findingsTitle: "Check",
  findingsIdle: "Not checked yet. Glossa checks a moment after you stop typing.",
  findingsValid: "Valid: a save would be accepted.",
  findingsValidNotes: (n: number) => `Valid: a save would be accepted, with ${n.toLocaleString()} note${n === 1 ? "" : "s"}.`,
  findingsInvalid: (n: number) => `${n.toLocaleString()} finding${n === 1 ? "" : "s"}: a save would be refused.`,
  findingsStale: "Changed since the last check.",
  checkFailed: "The document could not be checked. The save will check it again.",
  severity: { error: "Error", warning: "Warning", info: "Note" } as Record<string, string>,
  atLine: (n: number) => `line ${n}`,
  goToLine: (n: number) => `Go to line ${n}`,
  wholeDocument: "the whole document",
  gutterMark: (n: number, sev: string) => `Line ${n}: ${sev}`,

  // ── the chart ─────────────────────────────────────────────────────
  chart: {
    title: "Chart",
    stale: "Out of date: the text doesn't parse",
    empty: "Nothing to draw yet: the chart has no states.",
    drawing: "Drawing the chart…",
    failed: "The chart couldn't be drawn. The table below shows the same transitions.",
    tableCaption: "Every transition: from which state, on which event, under which guard, running which actions, to which state",
    from: "From",
    event: "On",
    guard: "If",
    actions: "Runs",
    to: "To",
    initial: "(start)",
    finals: (ids: string[]) => (ids.length ? `Final states: ${ids.join(", ")}.` : "No final state: the server refuses a chart that can never finish."),
  },

  // ── versions ──────────────────────────────────────────────────────
  versionsTitle: "Versions",
  versionsLead: "Every save is a new version; none is ever changed. Running instances stay on the version they started with.",
  versionsLoading: "Loading versions…",
  versionsFailed: "The versions could not be read.",
  versionRow: (n: number) => `Version ${n}`,
  latest: "latest",
  savedBy: (who: string, when: string) => `${who}, ${when}`,
  view: "View",
  viewOf: (n: number) => ` version ${n}`,

  // ── bindings ──────────────────────────────────────────────────────
  bindings: {
    title: "Workflow",
    lead: "Which workflow this project's work runs under. A binding names a workflow and, optionally, the locales and the namespace it is for. With no binding, nothing changes: translations are reviewed as before M5, without instances or assignments.",
    loading: "Loading bindings…",
    failed: "The bindings could not be read, so which workflow applies here is not known.",
    none: "No binding: this project's translations run under no workflow.",
    readOnly: "You can read this project's bindings. Binding and unbinding need the owner or admin role.",
    order:
      "Bindings are tried in this order: the one naming more fields (locales, namespace) first, and of two equally specific ones the later. The first that fits a translation is the one it runs under — the same rule as the check policy.",
    columns: { order: "Tried", workflow: "Workflow", subject: "Runs on", locales: "Locales", namespace: "Namespace", specificity: "Names", created: "Bound" },
    everyLocale: "Every locale",
    everyNamespace: "Every namespace",
    fields: (n: number) => (n === 0 ? "Nothing (the fallback)" : `${n} field${n === 1 ? "" : "s"}`),
    unknownDefinition: (id: string) => `Deleted workflow ${id.slice(0, 8)}`,
    remove: "Unbind",
    removeOf: (name: string) => ` ${name}`,
    removed: (name: string) => `Unbound ${name}. New work resolves without it; work in flight finishes where it is.`,
    add: "Bind a workflow",
    addTitle: "Bind a workflow",
    definition: "Workflow",
    choose: "Choose…",
    noDefinitions: "No workflow can be bound here yet. Write one first.",
    writeOne: "Write a workflow",
    locales: "Only these locales (optional)",
    localesHint: "None checked: every locale.",
    namespace: "Only this namespace (optional)",
    namespaceHint: "Empty: every namespace.",
    bind: "Bind",
    bound: (name: string) => `Bound ${name}. New work that fits it starts under its latest version.`,
    releaseDefault:
      "Release requests run on the workspace's release-approval workflow unless a binding for release requests replaces it. The environment's approval requirement holds whatever the chart says.",
    instances: "Instances",
    instancesLink: "See the work running under these bindings",
    resolveTitle: "Which workflow applies?",
    resolveLead: "Ask the server which binding a new translation would start under — the answer it would act on.",
    resolveSubject: "For",
    resolveLocale: "Locale",
    resolveNamespace: "Namespace (optional)",
    resolve: "Ask",
    resolving: "Asking…",
    resolvedNone: "No binding fits: no instance would be created. Translations are reviewed as before M5.",
    resolvedTo: (name: string, version: number) => `Runs under ${name}, starting on version ${version}.`,
    resolvedBinding: (n: number) => `Matched the binding tried ${ordinal(n)}.`,
  },
};

function ordinal(n: number): string {
  const s = ["th", "st", "nd", "rd"];
  const v = n % 100;
  return `${n}${s[(v - 20) % 10] ?? s[v] ?? s[0]}`;
}

/** Sentences for this surface's problem codes, merged into `strings.problems`. */
export const workflowProblems: Record<string, string> = {
  invalid_workflow: "The workflow document doesn't compile or lint. Nothing was saved; the findings say what to fix.",
  workflow_definition_exists: "A workflow with this name exists already. Open it and save a new version, or choose another name.",
  workflow_limit_reached: "This workspace keeps at most 50 workflows. Delete one you no longer use first.",
  workflow_binding_exists: "A binding for exactly these locales and this namespace exists already. Unbind it first to bind another workflow there.",
  invalid_workflow_binding: "That binding can't be made: a locale isn't a BCP 47 tag, or the namespace isn't one.",
  workflow_definition_out_of_scope: "That workflow belongs to another project, so it can't be bound here.",
  precondition_required: "This change needs the version it is based on. Reload and try again.",
};
