<script setup lang="ts">
import { ref, watch } from "vue";
import { useRouter } from "vue-router";
import { tenants } from "../api/endpoints";
import ErrorAlert from "../components/ErrorAlert.vue";
import { SLUG_PATTERN, slugify } from "../lib/slug";
import { refreshSession } from "../session/session";
import { strings } from "../strings";

const router = useRouter();
const name = ref("");
const slug = ref("");
const slugTouched = ref(false);
const busy = ref(false);
const error = ref<unknown>(null);

watch(name, (n) => {
  if (!slugTouched.value) slug.value = slugify(n);
});

async function submit(): Promise<void> {
  error.value = null;
  busy.value = true;
  try {
    const t = await tenants.create({ name: name.value.trim(), slug: slug.value });
    await refreshSession();
    await router.push({ name: "projects", params: { tenant: t.id } });
  } catch (e) {
    error.value = e;
  } finally {
    busy.value = false;
  }
}
</script>

<template>
  <div class="page">
    <section class="card stack narrow" aria-labelledby="org-h">
      <h1 id="org-h">{{ strings.tenants.createTitle }}</h1>
      <p class="muted">{{ strings.tenants.createLead }}</p>
      <ErrorAlert :error="error" />
      <form class="stack" @submit.prevent="submit">
        <div class="field">
          <label for="org-name">{{ strings.tenants.name }}</label>
          <input id="org-name" v-model="name" maxlength="200" required />
        </div>
        <div class="field">
          <label for="org-slug">{{ strings.tenants.slug }}</label>
          <input id="org-slug" v-model="slug" :pattern="SLUG_PATTERN" maxlength="63" aria-describedby="org-slug-hint" required @input="slugTouched = true" />
          <span id="org-slug-hint" class="hint">{{ strings.tenants.slugHint }}</span>
        </div>
        <div><button type="submit" class="btn btn-primary" :disabled="busy">{{ strings.tenants.create }}</button></div>
      </form>
    </section>
  </div>
</template>

<style scoped>
.narrow {
  max-inline-size: 32rem;
}
</style>
