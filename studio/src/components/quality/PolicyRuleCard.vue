<script setup lang="ts">
/**
 * One rule of the check policy (RFC 0005 §4.1), with its position in
 * the document treated as what it is: part of the meaning.
 *
 * Three things are always on screen, in words and not only in colour:
 * - **where it sits** — "Rule 2 of 5", as the legend of its own
 *   fieldset, inside an ordered list, so the position is structural and
 *   not a visual accident;
 * - **how specific it is** — the number precedence is decided by;
 * - **what its position decides** — either "nothing else here is
 *   equally specific and overlapping, so moving this changes no
 *   verdict", or the exact rules it wins or loses against.
 *
 * Reordering is two ordinary buttons. They are reachable with the
 * keyboard because they are buttons; the view moves focus with the rule
 * and announces the move in a live region, so the change is perceivable
 * without seeing the list jump.
 */
import { computed, ref } from "vue";
import type { ProjectLocale } from "../../api/schemas";
import { RULE_LAYERS, RULE_MODES, RULE_SEVERITIES, namedFields, type DraftRule, type RuleOrder } from "../../lib/policy";
import { strings } from "../../strings";

const props = defineProps<{
  rule: DraftRule;
  index: number;
  total: number;
  order: RuleOrder;
  locales: ProjectLocale[];
  disabled: boolean;
}>();
const emit = defineEmits<{
  update: [patch: Partial<DraftRule>];
  move: [delta: number];
  remove: [];
}>();
const s = strings.policy;
const q = strings.quality;

const up = ref<HTMLButtonElement>();
const down = ref<HTMLButtonElement>();

/** Keep the keyboard with the rule after a move; the end of the list has only one way to go. */
function focusMove(delta: number): void {
  const first = delta < 0 ? up.value : down.value;
  const other = delta < 0 ? down.value : up.value;
  (first && !first.disabled ? first : other)?.focus();
}
defineExpose({ focusMove });

const n = computed(() => props.index + 1);
const id = (field: string) => `rule-${props.rule.key}-${field}`;
const list = (ns: number[]) => s.ruleList(ns.map((i) => i + 1));

/** What the selector says, spelled out, so a rule reads as a sentence and not as five dropdowns. */
const selects = computed(() => {
  const fields = namedFields(props.rule);
  if (!fields.length) return s.ruleSelectsEverything;
  return fields.map(([k, v]) => `${s.ruleField[k] ?? k}: ${v}`).join(" · ");
});

const set = (patch: Partial<DraftRule>) => emit("update", patch);
const pick = (e: Event) => (e.target as HTMLSelectElement).value;
const typed = (e: Event) => (e.target as HTMLInputElement).value;
</script>

<template>
  <li class="rule card stack-sm" :data-index="index" data-testid="policy-rule">
    <!--
      The move controls sit beside the number they move, not at the
      bottom of the card: they act on "Rule 2 of 5", and putting them
      here also means the browser's focus scroll brings the rule's top
      into view rather than leaving it above the fold.
    -->
    <div class="head">
      <p class="heading">
        <span class="rule-number">{{ s.ruleHeading(n, total) }}</span>
        <span class="pill" :class="rule.severity === 'error' ? 'pill-err' : rule.severity === 'warning' ? 'pill-warn' : 'pill-neutral'" data-testid="rule-severity">
          {{ s.ruleSeverityName[rule.severity] ?? rule.severity }}
        </span>
        <span v-if="rule.mode === 'warn'" class="pill pill-accent" data-testid="rule-warn">{{ s.ruleModeName.warn }}</span>
      </p>
      <div class="actions">
        <button ref="up" type="button" class="btn btn-sm" :disabled="disabled || index === 0" data-testid="rule-up" @click="emit('move', -1)">
          {{ s.moveUp }}<span class="visually-hidden">{{ s.moveUpFor(n) }}</span>
        </button>
        <button ref="down" type="button" class="btn btn-sm" :disabled="disabled || index === total - 1" data-testid="rule-down" @click="emit('move', 1)">
          {{ s.moveDown }}<span class="visually-hidden">{{ s.moveDownFor(n) }}</span>
        </button>
        <button type="button" class="btn btn-sm btn-ghost" :disabled="disabled" data-testid="rule-remove" @click="emit('remove')">
          {{ s.removeRule }}<span class="visually-hidden">{{ s.removeRuleFor(n) }}</span>
        </button>
      </div>
    </div>

    <fieldset class="stack-sm">
      <legend class="visually-hidden">{{ s.ruleHeading(n, total) }}</legend>

      <p class="selects" data-testid="rule-selects">
        <span class="muted">{{ s.ruleSelects }}:</span> <span>{{ selects }}</span>
      </p>

      <div class="grid">
        <div class="field">
          <label :for="id('layer')">{{ s.ruleField.layer }}</label>
          <select :id="id('layer')" :value="rule.layer" :disabled="disabled" data-testid="rule-layer" @change="set({ layer: pick($event) })">
            <option value="">{{ s.ruleFieldAny }}</option>
            <option v-for="l in RULE_LAYERS" :key="l" :value="l">{{ q.layerName[l] ?? l }}</option>
          </select>
        </div>
        <div class="field">
          <label :for="id('code')">{{ s.ruleField.code }}</label>
          <input :id="id('code')" type="text" :value="rule.code" :placeholder="s.ruleCodePlaceholder" :disabled="disabled" @change="set({ code: typed($event) })" />
        </div>
        <div class="field">
          <label :for="id('locale')">{{ s.ruleField.locale }}</label>
          <select :id="id('locale')" :value="rule.locale" :disabled="disabled" data-testid="rule-locale" @change="set({ locale: pick($event) })">
            <option value="">{{ s.ruleFieldAny }}</option>
            <option v-for="l in locales" :key="l.code" :value="l.code">{{ l.code }}</option>
          </select>
        </div>
        <div class="field">
          <label :for="id('namespace')">{{ s.ruleField.namespace }}</label>
          <input
            :id="id('namespace')"
            type="text"
            :value="rule.namespace"
            :placeholder="s.ruleNamespacePlaceholder"
            :disabled="disabled"
            @change="set({ namespace: typed($event) })"
          />
        </div>
        <div class="field">
          <label :for="id('environment')">{{ s.ruleField.environment }}</label>
          <input
            :id="id('environment')"
            type="text"
            :value="rule.environment"
            :placeholder="s.ruleEnvironmentPlaceholder"
            :disabled="disabled"
            @change="set({ environment: typed($event) })"
          />
        </div>
        <div class="field">
          <label :for="id('severity')">{{ s.ruleSeverity }}</label>
          <select
            :id="id('severity')"
            :value="rule.severity"
            :disabled="disabled"
            :aria-describedby="rule.severity === 'off' ? id('off-hint') : undefined"
            data-testid="rule-severity-select"
            @change="set({ severity: pick($event) as DraftRule['severity'] })"
          >
            <option v-for="sev in RULE_SEVERITIES" :key="sev" :value="sev">{{ s.ruleSeverityName[sev] ?? sev }}</option>
          </select>
          <span v-if="rule.severity === 'off'" :id="id('off-hint')" class="hint">{{ s.ruleOffHint }}</span>
        </div>
        <div class="field">
          <label :for="id('mode')">{{ s.ruleMode }}</label>
          <select
            :id="id('mode')"
            :value="rule.mode"
            :disabled="disabled"
            :aria-describedby="rule.mode === 'warn' ? id('mode-hint') : undefined"
            data-testid="rule-mode"
            @change="set({ mode: pick($event) as DraftRule['mode'] })"
          >
            <option v-for="m in RULE_MODES" :key="m" :value="m">{{ s.ruleModeName[m] ?? m }}</option>
          </select>
          <span v-if="rule.mode === 'warn'" :id="id('mode-hint')" class="hint" data-testid="rule-warn-hint">{{ s.ruleModeWarnHint }}</span>
        </div>
      </div>

      <!-- ── what this rule's place in the list decides ────────────── -->
      <p class="hint" data-testid="rule-specificity">{{ s.specificity(order.specificity) }}</p>
      <p v-if="order.shadowedBy !== undefined" class="field-error" data-testid="rule-shadowed">{{ s.orderShadowed(order.shadowedBy + 1) }}</p>
      <template v-else>
        <p v-if="order.beatenBy.length" class="hint" data-testid="rule-beaten">{{ s.orderBeatenBy(order.beatenBy[order.beatenBy.length - 1]! + 1) }}</p>
        <p v-if="order.beats.length" class="hint" data-testid="rule-beats">{{ s.orderBeats(list(order.beats)) }}</p>
        <p v-if="!order.ties.length" class="hint" data-testid="rule-order-free">{{ s.orderFree }}</p>
      </template>
      <p v-if="order.advisoryError" class="field-error" role="alert" data-testid="rule-advisory">{{ s.ruleAdvisoryError }}</p>
    </fieldset>
  </li>
</template>

<style scoped>
.rule {
  padding: var(--kl-space-4);
}
fieldset {
  border: none;
  margin: 0;
  padding: 0;
  min-inline-size: 0;
}
.head {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-3);
  align-items: baseline;
  justify-content: space-between;
}
.heading {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-2);
  align-items: baseline;
}
.rule-number {
  font-weight: var(--kl-weight-semibold);
}
.selects {
  overflow-wrap: anywhere;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(11rem, 1fr));
  gap: var(--kl-space-3);
}
.actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--kl-space-2);
}
/*
 * Moving a rule moves the keyboard with it, and the browser scrolls the
 * button it lands on into view. The top bar is sticky, so without this
 * the control can arrive underneath it — reachable, and half covered.
 */
.actions .btn {
  scroll-margin-block-start: calc(var(--gs-topbar-h) + var(--kl-space-4));
}
</style>
