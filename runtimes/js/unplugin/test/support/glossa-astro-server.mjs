// Stands in for @klarlabs-studio/glossa-astro/server in the fixture builds: the fixtures are
// about where messages are used, not how they render.
export function getGlossa() {
  return { t: (id) => id, locale: "de", dir: "ltr" };
}
