<script setup lang="ts">
import { RouterLink } from "vue-router";
import { strings } from "../strings";
</script>

<template>
  <div class="page stack">
    <h1>{{ strings.app.notFound }}</h1>
    <p><RouterLink :to="{ name: 'home' }">{{ strings.app.backHome }}</RouterLink></p>
  </div>
</template>
