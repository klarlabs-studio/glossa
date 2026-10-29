<script setup lang="ts">
/**
 * An environment's settings: what ships (its policy) and, for a standard
 * environment, who must approve a publish or promote first (RFC 0006
 * §5.1). Both change together, under one `If-Match`.
 *
 * Changing who approves is governance, so it also needs
 * `workflows.manage`; a policy-only change goes through the ordinary
 * policy edit and leaves the requirement as it is. The requirement is
 * never the requester (`distinct_from_requester` is always true): that is
 * shown as fixed, not offered as a choice.
 */
import { computed, reactive, ref, shallowRef, watch } from "vue";
import { members as membersApi } from "../../api/endpoints";
import { isApiError } from "../../api/errors";
import { useReleaseOps } from "../../api/release-ops";
import type { ProjectRef, ReleasesPort } from "../../api/releases";
import type { Environment, EnvironmentApproval, Member, Role, ShippableState } from "../../api/schemas";
import { useWork } from "../../api/work";
import type { Group } from "../../api/work-schemas";
import { sameApproval } from "../../lib/release-ops";
import { SHIPPABLE_STATES } from "../../lib/releases";
import { strings } from "../../strings";
import ErrorAlert from "../ErrorAlert.vue";
import ModalDialog from "../ModalDialog.vue";

const props = defineProps<{
  open: boolean;
  port: ReleasesPort;
  project: ProjectRef;
  environment: string;
  /** `workflows.manage`: needed to change who must approve. */
  canGovern?: boolean;
}>();
const emit = defineEmits<{ close: []; done: [message: string] }>();
const s = strings.releases;
const ro = strings.releaseOps;
const ops = useReleaseOps();
const work = useWork();

const ROLES: Role[] = ["reviewer", "admin", "owner", "developer", "translator"];
type PartyKind = "role" | "group" | "member";

const form = reactive({ states: [] as ShippableState[], include_outdated: true });
const approval = reactive({ on: false, n: 1, kind: "role" as PartyKind, role: "reviewer" as Role, group: "", member: "" });
const env = shallowRef<Environment>();
const etag = ref<string>();
const loading = ref(false);
const busy = ref(false);
const error = ref<unknown>(null);
const approvalError = ref("");
const groups = shallowRef<Group[] | undefined>();
const memberList = shallowRef<Member[] | undefined>();
const groupsFailed = ref(false);
const membersFailed = ref(false);

const branch = computed(() => env.value?.kind === "branch");
const wanted = computed<EnvironmentApproval | undefined>(() => {
  if (!approval.on) return undefined;
  const from = approval.kind === "role" ? { role: approval.role } : approval.kind === "group" ? { group: approval.group } : { member: approval.member };
  return { n: approval.n, from, distinct_from_requester: true };
});
const approvalChanged = computed(() => !branch.value && !sameApproval(wanted.value, env.value?.approval));
const partyChosen = computed(() => !approval.on || (approval.kind === "role" ? !!approval.role : approval.kind === "group" ? !!approval.group : !!approval.member));
const countValid = computed(() => !approval.on || (Number.isInteger(approval.n) && approval.n >= 1 && approval.n <= 10));
const valid = computed(() => form.states.length > 0 && partyChosen.value && countValid.value && (!approvalChanged.value || !!props.canGovern));

function adopt(e: Environment): void {
  env.value = e;
  form.states = [...e.policy.states];
  form.include_outdated = e.policy.include_outdated;
  const a = e.approval;
  approval.on = !!a;
  approval.n = a?.n ?? 1;
  approval.kind = a?.from.group ? "group" : a?.from.member ? "member" : "role";
  approval.role = a?.from.role ?? "reviewer";
  approval.group = a?.from.group ?? "";
  approval.member = a?.from.member ?? "";
}

async function loadParties(): Promise<void> {
  const [gs, ms] = await Promise.allSettled([work.groups(props.project.tenant), membersApi.list(props.project.tenant)]);
  groups.value = gs.status === "fulfilled" ? gs.value : undefined;
  groupsFailed.value = gs.status === "rejected";
  memberList.value = ms.status === "fulfilled" ? ms.value.filter((m) => m.status === "active") : undefined;
  membersFailed.value = ms.status === "rejected";
}

async function load(): Promise<void> {
  loading.value = true;
  try {
    const r = await props.port.environment(props.project, props.environment);
    adopt(r.value);
    etag.value = r.etag;
    if (r.value.kind !== "branch") void loadParties();
  } catch (e) {
    error.value = e;
  } finally {
    loading.value = false;
  }
}

watch(
  () => props.open,
  (open) => {
    if (!open) return;
    error.value = null;
    approvalError.value = "";
    void load();
  },
  { immediate: true },
);

/** A group named by id or by name (the API accepts both). */
const groupLabel = (g: Group) => g.name;
const memberLabel = (m: Member) => m.display_name || m.email;

async function save(): Promise<void> {
  if (!valid.value || !etag.value) return;
  busy.value = true;
  error.value = null;
  approvalError.value = "";
  const policy = { states: SHIPPABLE_STATES.filter((st) => form.states.includes(st)), include_outdated: form.include_outdated };
  try {
    if (approvalChanged.value) {
      const w = wanted.value;
      await ops.configureEnvironment(props.project, props.environment, w ? { policy, approval: w } : { policy, clear_approval: true }, etag.value);
      emit("done", ro.settingsSaved(props.environment));
    } else {
      await props.port.updatePolicy(props.project, props.environment, policy, etag.value);
      emit("done", s.policySaved(props.environment));
    }
  } catch (e) {
    if (isApiError(e, "invalid_approval")) approvalError.value = ro.approvalChoose;
    else error.value = e;
    if (isApiError(e, "precondition_failed")) await load();
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <ModalDialog :open="open" :title="s.policyTitle(environment)" @close="emit('close')">
    <p v-if="loading" class="muted" role="status">{{ strings.app.loading }}</p>
    <form v-else id="policy-form" class="stack" @submit.prevent="save">
      <fieldset class="stack-sm" aria-describedby="pol-states-hint">
        <legend class="label">{{ s.policyStates }}</legend>
        <label v-for="st in SHIPPABLE_STATES" :key="st" class="check">
          <input v-model="form.states" type="checkbox" :value="st" :disabled="busy" />
          <span>{{ s.state[st] }}</span>
        </label>
        <span id="pol-states-hint" class="hint">{{ s.policyStatesHint }}</span>
        <span v-if="!form.states.length" class="field-error" role="alert">{{ s.policyNeedsState }}</span>
      </fieldset>
      <label class="check">
        <input v-model="form.include_outdated" type="checkbox" :disabled="busy" aria-describedby="pol-outdated-hint" />
        <span class="stack-sm">
          <span>{{ s.includeOutdated }}</span>
          <span id="pol-outdated-hint" class="hint">{{ s.includeOutdatedHint }}</span>
        </span>
      </label>

      <p v-if="branch" class="hint" data-testid="approval-branch">{{ ro.approvalBranch }}</p>
      <fieldset v-else class="stack-sm approval" aria-describedby="pol-approval-lead" data-testid="approval-settings">
        <legend class="label">{{ ro.approvalTitle }}</legend>
        <p id="pol-approval-lead" class="hint">{{ ro.approvalLead }}</p>
        <p v-if="!canGovern" class="alert" data-testid="approval-needs-manage">{{ ro.approvalNeedsManage }}</p>
        <label class="check">
          <input v-model="approval.on" type="radio" :value="false" name="pol-approval" :disabled="busy || !canGovern" data-testid="approval-off" />
          <span>{{ ro.approvalOff }}</span>
        </label>
        <label class="check">
          <input v-model="approval.on" type="radio" :value="true" name="pol-approval" :disabled="busy || !canGovern" data-testid="approval-on" />
          <span>{{ ro.approvalOn }}</span>
        </label>
        <div v-if="approval.on" class="stack-sm nested">
          <div class="field">
            <label for="pol-n">{{ ro.approvalCount }}</label>
            <input id="pol-n" v-model.number="approval.n" type="number" min="1" max="10" step="1" :disabled="busy || !canGovern" data-testid="approval-n" />
          </div>
          <div class="field">
            <label for="pol-kind">{{ ro.approvalFrom }}</label>
            <select id="pol-kind" v-model="approval.kind" :disabled="busy || !canGovern" data-testid="approval-kind">
              <option value="role">{{ ro.approvalFromRole }}</option>
              <option value="group">{{ ro.approvalFromGroup }}</option>
              <option value="member">{{ ro.approvalFromMember }}</option>
            </select>
          </div>
          <div v-if="approval.kind === 'role'" class="field">
            <label for="pol-role">{{ ro.approvalRole }}</label>
            <select id="pol-role" v-model="approval.role" :disabled="busy || !canGovern" data-testid="approval-role">
              <option v-for="r in ROLES" :key="r" :value="r">{{ r }}</option>
            </select>
          </div>
          <div v-else-if="approval.kind === 'group'" class="field">
            <label for="pol-group">{{ ro.approvalGroup }}</label>
            <select id="pol-group" v-model="approval.group" :disabled="busy || !canGovern || groupsFailed" data-testid="approval-group">
              <option value="" disabled>{{ s.choose }}</option>
              <option v-if="approval.group && !groups?.some((g) => g.id === approval.group || g.name === approval.group)" :value="approval.group">{{ approval.group }}</option>
              <option v-for="g in groups ?? []" :key="g.id" :value="g.id">{{ groupLabel(g) }}</option>
            </select>
            <span v-if="groupsFailed" class="field-error" role="alert">{{ ro.approvalGroupsFailed }}</span>
          </div>
          <div v-else class="field">
            <label for="pol-member">{{ ro.approvalMember }}</label>
            <select id="pol-member" v-model="approval.member" :disabled="busy || !canGovern || membersFailed" data-testid="approval-member">
              <option value="" disabled>{{ s.choose }}</option>
              <option v-if="approval.member && !memberList?.some((m) => m.id === approval.member)" :value="approval.member">{{ approval.member }}</option>
              <option v-for="m in memberList ?? []" :key="m.id" :value="m.id">{{ memberLabel(m) }}</option>
            </select>
            <span v-if="membersFailed" class="field-error" role="alert">{{ ro.approvalMembersFailed }}</span>
          </div>
          <span v-if="!partyChosen" class="field-error" role="alert">{{ ro.approvalChoose }}</span>
          <label class="check">
            <input type="checkbox" checked disabled aria-describedby="pol-distinct-hint" data-testid="approval-distinct" />
            <span id="pol-distinct-hint" class="hint">{{ ro.distinctFixed }}</span>
          </label>
        </div>
        <p v-if="approvalError" class="field-error" role="alert" data-testid="approval-error">{{ approvalError }}</p>
      </fieldset>
    </form>
    <ErrorAlert :error="error" />
    <template #actions>
      <button type="button" class="btn" :disabled="busy" @click="emit('close')">{{ strings.app.cancel }}</button>
      <button type="submit" form="policy-form" class="btn btn-primary" :disabled="busy || loading || !valid">{{ s.savePolicy }}</button>
    </template>
  </ModalDialog>
</template>

<style scoped>
fieldset {
  border: none;
  margin: 0;
  padding: 0;
}
.approval {
  border-block-start: 1px solid var(--kl-border);
  padding-block-start: var(--kl-space-3);
}
.nested {
  padding-inline-start: var(--kl-space-5);
}
</style>
