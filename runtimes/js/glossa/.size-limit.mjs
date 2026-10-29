// The size budgets of every subpath (RFC 0005 §5.1 and the runtimes' own), measured on dist/.
// `ignore: ["@klarlabs-studio/glossa"]` means the runtime ONLY: esbuild's `external` would also
// drop `@klarlabs-studio/glossa/<anything>`, the package's own subpaths, so the exact specifier
// is externalised by a plugin instead. Everything else in `ignore` is a plain external.
const RUNTIME = "@klarlabs-studio/glossa";

const runtimeIsExternal = {
  name: "glossa-runtime-external",
  setup(build) {
    build.onResolve({ filter: /^@klarlabs-studio\/glossa$/ }, () => ({ path: RUNTIME, external: true }));
  },
};

const checks = [
  {
    "name": "@klarlabs-studio/glossa, interpreter: { format, formatToParts }",
    "path": "dist/runtime/index.js",
    "import": "{ format, formatToParts }",
    "limit": "4 KB"
  },
  {
    "name": "@klarlabs-studio/glossa, runtime: { createRuntime }",
    "path": "dist/runtime/index.js",
    "import": "{ createRuntime }",
    "limit": "6.8 KB"
  },
  {
    "name": "@klarlabs-studio/glossa, runtime + resolver chain",
    "path": "dist/runtime/index.js",
    "import": "{ createRuntime, resolveLocales, acceptLanguage }",
    "limit": "6.9 KB"
  },
  {
    "name": "@klarlabs-studio/glossa/idb",
    "path": "dist/runtime/idb.js",
    "limit": "0.5 KB"
  },
  {
    "name": "@klarlabs-studio/glossa/apierr (parse an apierr envelope, resolve it with a runtime)",
    "path": "dist/runtime/apierr.js",
    "limit": "0.5 KB"
  },
  {
    "name": "@klarlabs-studio/glossa/dev,the overlay loader and its popup sign-in (never in production builds)",
    "path": "dist/runtime/dev.js",
    "limit": "1.9 KB"
  },
  {
    "name": "@klarlabs-studio/glossa/elements, everything included (lit, @lit/context, the runtime)",
    "path": "dist/elements/index.js",
    "limit": "15.3 KB"
  },
  {
    "name": "@klarlabs-studio/glossa/elements, own code (lit, @lit/context and the runtime excluded)",
    "path": "dist/elements/index.js",
    "ignore": [
      "lit",
      "@lit/context",
      "@klarlabs-studio/glossa"
    ],
    "limit": "3.5 KB"
  },
  {
    "name": "@klarlabs-studio/glossa/elements/ssr (the runtime excluded)",
    "path": "dist/elements/ssr.js",
    "ignore": [
      "@klarlabs-studio/glossa"
    ],
    "limit": "1.75 KB"
  },
  {
    "name": "@klarlabs-studio/glossa/vue, own code (vue and the runtime excluded)",
    "path": "dist/vue/index.js",
    "ignore": [
      "vue",
      "@klarlabs-studio/glossa"
    ],
    "limit": "1.5 KB"
  },
  {
    "name": "@klarlabs-studio/glossa/vue with the runtime (vue excluded)",
    "path": "dist/vue/index.js",
    "ignore": [
      "vue"
    ],
    "limit": "8 KB"
  },
  {
    "name": "@klarlabs-studio/glossa/react, own code (react and the runtime excluded)",
    "path": "dist/react/index.js",
    "ignore": [
      "react",
      "@klarlabs-studio/glossa"
    ],
    "limit": "1.5 KB"
  },
  {
    "name": "@klarlabs-studio/glossa/react with the runtime (react excluded)",
    "path": "dist/react/index.js",
    "ignore": [
      "react"
    ],
    "limit": "8 KB"
  },
  {
    "name": "@klarlabs-studio/glossa/astro/client, the islands' page runtime (the runtime excluded)",
    "path": "dist/astro/client.js",
    "ignore": [
      "@klarlabs-studio/glossa",
      "virtual:glossa/config"
    ],
    "limit": "0.75 KB"
  },
  {
    "name": "@klarlabs-studio/glossa/capture, a capture session WITH the visual probe pass, what `glossa capture` runs (RFC 0005 §5.1's 4 kB)",
    "path": [
      "dist/capture/index.js",
      "dist/capture/probes.js"
    ],
    "import": {
      "dist/capture/index.js": "{ startCapture }",
      "dist/capture/probes.js": "{ probe }"
    },
    "limit": "4 KB"
  },
  {
    "name": "@klarlabs-studio/glossa/capture, a session WITHOUT it, what the overlay pulls in (the runtime is types only)",
    "path": "dist/capture/index.js",
    "limit": "3 KB"
  },
  {
    "name": "@klarlabs-studio/glossa/overlay, everything included (lit, capture; the runtime is types only)",
    "path": "dist/overlay/index.js",
    "limit": "16 KB"
  },
  {
    "name": "@klarlabs-studio/glossa/overlay, own code (lit and capture excluded)",
    "path": "dist/overlay/index.js",
    "ignore": [
      "lit",
      "@klarlabs-studio/glossa/capture"
    ],
    "limit": "8.5 KB"
  },
  {
    "name": "the served bundle, /overlay/v1/overlay.js (@klarlabs-studio/glossa/overlay/bundle)",
    "path": "dist/overlay/bundle/overlay.js",
    "limit": "16 KB"
  }
];

export default checks.map((check) => {
  if (!check.ignore?.includes(RUNTIME)) return check;
  return {
    ...check,
    ignore: check.ignore.filter((name) => name !== RUNTIME),
    modifyEsbuildConfig: (config) => ({ ...config, plugins: [...(config.plugins ?? []), runtimeIsExternal] }),
  };
});
