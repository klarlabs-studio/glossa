<script setup lang="ts">
/** One import of the project and its results (see ImportJobDetail). */
import { computed } from "vue";
import { useRoute } from "vue-router";
import ImportJobDetail from "../../components/integration/ImportJobDetail.vue";
import { strings } from "../../strings";
import { useProject } from "./context";

const route = useRoute();
const { tenant, projectId, grant } = useProject();
const jobId = computed(() => String(route.params.job));
const jobLink = (job: string) => ({ name: "import-job", params: { tenant: tenant.value, project: projectId.value, job } });
</script>

<template>
  <ImportJobDetail
    :tenant="tenant"
    :job-id="jobId"
    :grant="grant"
    :job-link="jobLink"
    :back="{ name: 'files', params: { tenant, project: projectId } }"
    :back-label="strings.integration.wizard.back"
  />
</template>
