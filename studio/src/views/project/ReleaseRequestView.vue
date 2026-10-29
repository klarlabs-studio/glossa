<script setup lang="ts">
/**
 * One release request (RFC 0006 §5.1): what it would deploy where, who
 * asked, what the completeness requirement said, whether the requester
 * forced past it and why, the requirement its approvers were asked for,
 * and every decision so far — then the approver's decision, or the
 * requester's withdrawal.
 *
 * Approving is a person's decision and four-eyes: the requester never
 * counts, so the requester is shown why there is nothing for them to
 * decide rather than a button the server would refuse. Force overrides
 * the gate and never the approval, so a forced request says so where
 * the approver decides.
 */
import { computed, nextTick, ref, shallowRef, useTemplateRef, watch } from "vue";
import { RouterLink, useRoute } from "vue-router";
import { isApiError } from "../../api/errors";
import { useReleaseOps } from "../../api/release-ops";
import type { ReleaseRequest } from "../../api/release-ops-schemas";
import { useReleases } from "../../api/releases";
import type { Approval } from "../../api/work-schemas";
import type { WorkflowInstance } from "../../api/workflows-schemas";
import { useWorkflows } from "../../api/workflows";
import ErrorAlert from "../../components/ErrorAlert.vue";
import ModalDialog from "../../components/ModalDialog.vue";
import { usePartyNames } from "../../components/releases/party-names";
import { grants, partyText } from "../../lib/release-ops";
import { absoluteTime, relativeTime } from "../../lib/time";
import { allows } from "../../session/permissions";
import { usePeople } from "../../session/people";
import { useSession } from "../../session/session";
import { problemText, strings } from "../../strings";
import { useProject } from "./context";

const ro = strings.releaseOps;
const s = strings.releases;
const route = useRoute();
const ops = useReleaseOps();
const releases = useReleases();
const workflows = useWorkflows();
const { tenant, projectId, grant } = useProject();
const person = usePeople(() => tenant.value);
const names = usePartyNames(() => tenant.value);
const { person: me } = useSession();
const self = computed(() => (me.value ? `person:${me.value.id}` : ""));
const project = () => ({ tenant: tenant.value, project: projectId.value });
const id = computed(() => String(route.params.request));

const canPublish = computed(() => allows(grant.value, "releases.publish"));
/** Environment-scoped for release subjects: held in any locale is enough (RFC 0006 §5.1 amendment). */
const canDecide = computed(() => grant.value.get("approvals.decide") !== undefined);

type Phase = "loading" | "ready" | "failed";
const phase = ref<Phase>("loading");
const loadError = ref<unknown>(null);
const request = shallowRef<ReleaseRequest>();
const version = ref<number>();
type ApprovalState = { state: "loading" } | { state: "ready"; approval: Approval | undefined } | { state: "failed"; error: unknown };
const approval = shallowRef<ApprovalState>({ state: "loading" });
const instances = shallowRef<WorkflowInstance[]>([]);
const reason = ref("");
const busy = ref(false);
const status = ref("");
const decideError = ref("");
const notAsked = ref(false);
const withdrawing = ref(false);
const withdrawReason = ref("");
const withdrawError = ref<unknown>(null);
const heading = useTemplateRef<HTMLHeadingElement>("heading");

async function loadApproval(): Promise<void> {
  approval.value = { state: "loading" };
  try {
    approval.value = { state: "ready", approval: await ops.requestApproval(project(), id.value) };
  } catch (error) {
    approval.value = { state: "failed", error };
  }
}

async function load(): Promise<void> {
  phase.value = "loading";
  loadError.value = null;
  try {
    const r = await ops.releaseRequest(project(), id.value);
    request.value = r;
    phase.value = "ready";
    void loadApproval();
    void releases
      .release(project(), r.release_id)
      .then((rel) => (version.value = rel.version))
      .catch(() => undefined);
    // Best effort: the workflow instance that carries the request.
    void workflows
      .instances(project(), { subject_id: r.id })
      .then((page) => (instances.value = page.items))
      .catch(() => (instances.value = []));
  } catch (e) {
    loadError.value = e;
    phase.value = "failed";
  }
}
watch([projectId, id], load, { immediate: true });

const label = computed(() => (version.value === undefined ? "…" : s.version(version.value)));
const party = computed(() => (request.value ? partyText(request.value.approval.from, names.value) : ""));
const current = computed(() => (approval.value.state === "ready" ? approval.value.approval : undefined));
const granted = computed(() => (current.value ? grants(current.value) : 0));
const pending = computed(() => request.value?.state === "pending");
const mine = computed(() => !!self.value && request.value?.requester === self.value);
const decided = computed(() => !!current.value?.decisions.some((d) => d.principal === self.value));
const decidable = computed(() => pending.value && canDecide.value && !mine.value && !decided.value);

/** The release-specific sentence for the refusals a release decision can meet. */
function refusal(e: unknown): string {
  if (isApiError(e, "own_text")) return ro.ownRefusal;
  if (isApiError(e, "not_eligible")) return ro.notEligible;
  if (isApiError(e, "person_required")) return ro.personRequired;
  return problemText(e);
}

async function decide(decision: "granted" | "denied"): Promise<void> {
  const r = request.value;
  if (!r || busy.value) return;
  busy.value = true;
  status.value = "";
  decideError.value = "";
  notAsked.value = false;
  try {
    await ops.decide(project(), r.id, decision, reason.value.trim() || undefined);
    reason.value = "";
    status.value = (decision === "granted" ? ro.approved : ro.denied)(label.value, r.environment);
    await load();
  } catch (e) {
    if (isApiError(e, "approval_not_requested")) {
      notAsked.value = true;
    } else if (isApiError(e, "release_request_closed") || isApiError(e, "approval_closed") || isApiError(e, "approval_superseded")) {
      await load();
      status.value = ro.closedNow;
    } else {
      decideError.value = refusal(e);
    }
  } finally {
    busy.value = false;
    await nextTick();
    heading.value?.focus();
  }
}

async function withdraw(): Promise<void> {
  const r = request.value;
  if (!r || busy.value) return;
  busy.value = true;
  withdrawError.value = null;
  try {
    await ops.withdraw(project(), r.id, withdrawReason.value.trim() || undefined);
    withdrawing.value = false;
    status.value = ro.withdrawn;
    await load();
  } catch (e) {
    if (isApiError(e, "release_request_closed")) {
      withdrawing.value = false;
      await load();
      status.value = ro.closedNow;
    } else withdrawError.value = e;
  } finally {
    busy.value = false;
  }
}
function openWithdraw(): void {
  withdrawReason.value = "";
  withdrawError.value = null;
  withdrawing.value = true;
}
</script>

<template>
  <div class="page stack request">
    <RouterLink :to="{ name: 'release-requests', params: { tenant, project: projectId } }" class="muted">{{ ro.backToRequests }}</RouterLink>
    <p role="status" aria-live="polite" data-testid="request-status">{{ status }}</p>

    <p v-if="phase === 'loading'" class="muted" data-testid="request-loading">{{ ro.requestLoading }}</p>
    <div v-else-if="phase === 'failed'" class="alert alert-error stack-sm" role="alert" data-testid="request-failed">
      <p class="alert-title">{{ ro.requestFailed }}</p>
      <p>{{ problemText(loadError) }}</p>
      <div><button type="button" class="btn btn-sm" @click="load">{{ ro.retry }}</button></div>
    </div>
    <template v-else-if="request">
      <div class="page-header">
        <div class="stack-sm">
          <h1 ref="heading" tabindex="-1">{{ ro.requestTitle(label, request.environment) }}</h1>
          <p class="row">
            <span class="pill" :class="`pill-${ro.stateTone[request.state] ?? 'neutral'}`" data-testid="request-state">{{ ro.state[request.state] ?? request.state }}</span>
            <span class="muted">{{ ro.what(ro.action[request.action] ?? request.action, label, request.environment) }}</span>
          </p>
          <p class="muted" data-testid="request-requester">
            {{ ro.requestedBy(person(request.requester)) }},
            <time :datetime="request.created_at" :title="absoluteTime(request.created_at)">{{ relativeTime(request.created_at) }}</time>
          </p>
          <p v-if="!pending && request.decided_by" class="muted" data-testid="request-closed">
            {{ ro.closedBy(ro.state[request.state] ?? request.state, person(request.decided_by)) }}
            <template v-if="request.reason"> · {{ ro.closedReason(request.reason) }}</template>
          </p>
        </div>
        <button v-if="pending && canPublish" type="button" class="btn" data-testid="request-withdraw" @click="openWithdraw">{{ ro.withdraw }}</button>
      </div>

      <div v-if="request.forced" class="alert alert-error stack-sm" data-testid="request-forced">
        <p class="alert-title">{{ ro.forced }}</p>
        <p v-if="request.force_reason">{{ ro.forcedReason(request.force_reason) }}</p>
      </div>

      <section class="card stack-sm" aria-labelledby="rr-gate-h">
        <h2 id="rr-gate-h">{{ ro.gate }}</h2>
        <p v-if="request.gate.met" data-testid="request-gate">{{ ro.gateMet }}</p>
        <div v-else data-testid="request-gate">
          <p>{{ ro.gateUnmet }}</p>
          <ul>
            <li v-for="(u, i) in request.gate.unmet ?? []" :key="i">{{ u }}</li>
          </ul>
        </div>
      </section>

      <section class="card stack-sm" aria-labelledby="rr-approvals-h">
        <h2 id="rr-approvals-h">{{ ro.approvals }}</h2>
        <p data-testid="request-requirement"><span class="label">{{ ro.requirement }}:</span> {{ ro.requirementText(request.approval.n, party) }}</p>
        <p v-if="approval.state === 'loading'" class="muted">{{ ro.approvalLoading }}</p>
        <div v-else-if="approval.state === 'failed'" class="alert alert-error" role="alert" data-testid="request-approval-failed">
          <p>{{ ro.approvalFailed }}</p>
          <p>{{ problemText(approval.error) }}</p>
        </div>
        <template v-else>
          <p v-if="!current" class="muted" data-testid="request-not-asked">{{ ro.notAskedYet }}</p>
          <template v-else>
            <p data-testid="request-progress">{{ ro.progress(granted, request.approval.n) }}</p>
            <p v-if="!current.decisions.length" class="muted">{{ ro.noDecisions }}</p>
            <ul v-else class="decisions" data-testid="request-decisions">
              <li v-for="(d, i) in current.decisions" :key="i" :data-decision="d.decision">
                <span class="pill" :class="d.decision === 'granted' ? 'pill-ok' : 'pill-err'">{{ ro.decision[d.decision] }}</span>
                {{ ro.by(person(d.principal)) }},
                <time :datetime="d.at" :title="absoluteTime(d.at)">{{ relativeTime(d.at) }}</time>
                <span v-if="d.reason" class="muted"> — {{ ro.reasonText(d.reason) }}</span>
              </li>
            </ul>
          </template>
        </template>
      </section>

      <section v-if="pending" class="card stack-sm" aria-labelledby="rr-decide-h" data-testid="request-decide">
        <h2 id="rr-decide-h">{{ ro.yourDecision }}</h2>
        <p v-if="mine" class="alert alert-warn" data-testid="request-own">{{ ro.ownRequest }}</p>
        <p v-else-if="!canDecide" class="alert" data-testid="request-cannot-decide">{{ ro.cannotDecide }}</p>
        <p v-else-if="decided" class="muted" data-testid="request-decided">{{ ro.alreadyDecided }}</p>
        <template v-else>
          <p v-if="request.forced" class="hint">{{ ro.forced }}</p>
          <div v-if="notAsked" class="alert alert-warn stack-sm" role="alert" data-testid="request-not-requested">
            <p>{{ ro.notRequested }}</p>
            <div><button type="button" class="btn btn-sm" @click="loadApproval">{{ ro.retry }}</button></div>
          </div>
          <p v-if="decideError" class="alert alert-error" role="alert" data-testid="request-error">{{ decideError }}</p>
          <div class="field">
            <label for="rr-reason">{{ ro.reason }}</label>
            <textarea id="rr-reason" v-model="reason" rows="2" maxlength="2000" data-testid="request-reason" />
          </div>
          <div class="row">
            <button type="button" class="btn btn-primary" :disabled="busy || !decidable" data-testid="request-approve" @click="decide('granted')">{{ ro.approve }}</button>
            <button type="button" class="btn btn-danger" :disabled="busy || !decidable" data-testid="request-deny" @click="decide('denied')">{{ ro.deny }}</button>
          </div>
        </template>
      </section>

      <section v-if="instances.length" class="stack-sm" aria-labelledby="rr-wf-h" data-testid="request-instances">
        <h2 id="rr-wf-h">{{ ro.instances }}</h2>
        <ul>
          <li v-for="i in instances" :key="i.id">
            <RouterLink :to="{ name: 'workflow-instance', params: { tenant, project: projectId, instance: i.id } }">{{ ro.instanceLink(i.state) }}</RouterLink>
          </li>
        </ul>
      </section>
    </template>

    <ModalDialog :open="withdrawing" :title="ro.withdrawTitle" @close="withdrawing = false">
      <p>{{ ro.withdrawLead }}</p>
      <form id="rr-withdraw" class="stack-sm" @submit.prevent="withdraw">
        <div class="field">
          <label for="rr-withdraw-reason">{{ ro.withdrawReason }}</label>
          <textarea id="rr-withdraw-reason" v-model="withdrawReason" rows="2" maxlength="1000" data-testid="withdraw-reason" />
        </div>
      </form>
      <ErrorAlert :error="withdrawError" />
      <template #actions>
        <button type="button" class="btn" :disabled="busy" @click="withdrawing = false">{{ strings.app.cancel }}</button>
        <button type="submit" form="rr-withdraw" class="btn btn-danger" :disabled="busy" data-testid="withdraw-confirm">{{ ro.withdrawConfirm }}</button>
      </template>
    </ModalDialog>
  </div>
</template>

<style scoped>
.request {
  max-inline-size: var(--kl-content-lg, 64rem);
}
.decisions {
  margin: 0;
  padding-inline-start: var(--kl-space-4);
  display: flex;
  flex-direction: column;
  gap: var(--kl-space-1);
}
textarea {
  inline-size: 100%;
}
</style>
