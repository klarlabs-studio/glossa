// Modules the integration's Vite plugin provides.
declare module "virtual:glossa/config" {
  const config: import("./config.js").PublicConfig;
  export default config;
}

// Server-only: the whole release the build renders with.
declare module "virtual:glossa/release" {
  const release: import("@klarlabs-studio/glossa-runtime").BundledRelease | null;
  export default release;
}
