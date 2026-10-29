<script setup lang="ts">
/** One import into the workspace's memory or termbase and its results (see ImportJobDetail). */
import { computed } from "vue";
import { useRoute } from "vue-router";
import ImportJobDetail from "../../components/integration/ImportJobDetail.vue";
import { useGrant } from "../../session/session";
import { strings } from "../../strings";

const route = useRoute();
const tenant = computed(() => String(route.params.tenant));
const jobId = computed(() => String(route.params.job));
const grant = useGrant(() => tenant.value);
const jobLink = (job: string) => ({ name: "workspace-import-job", params: { tenant: tenant.value, job } });
</script>

<template>
  <ImportJobDetail
    :tenant="tenant"
    :job-id="jobId"
    :grant="grant"
    :job-link="jobLink"
    :back="{ name: 'workspace-knowledge', params: { tenant } }"
    :back-label="strings.tenantSettings.knowledge.backToWorkspace"
  />
</template>
