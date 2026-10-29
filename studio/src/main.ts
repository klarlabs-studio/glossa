import "@klarlabs-studio/ui/tokens.css";
import "@klarlabs-studio/ui/kl-badge.js";
import "@klarlabs-studio/ui/kl-theme-toggle.js";
import "./styles/studio.css";

import { createApp } from "vue";
import App from "./App.vue";
import { onSessionExpired } from "./api/client";
import { apiReleases, RELEASES } from "./api/releases";
import { createStudioRouter } from "./router";
import { sessionExpired } from "./session/session";

applyTheme();

const router = createStudioRouter();
onSessionExpired(() => {
  sessionExpired();
  const next = router.currentRoute.value.fullPath;
  void router.push({ name: "sign-in", query: { next } });
});

createApp(App)
  .use(router)
  .provide(RELEASES, apiReleases)
  .mount("#app");

/** The design system wants an explicit theme; kl-theme-toggle persists the choice under "kl-theme". */
function applyTheme(): void {
  let saved: string | null = null;
  try {
    saved = localStorage.getItem("kl-theme");
  } catch {
    // storage blocked
  }
  const theme = saved === "dark" || saved === "light" ? saved : matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
  document.documentElement.setAttribute("data-theme", theme);
}
