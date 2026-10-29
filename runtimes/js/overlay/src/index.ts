// Importing this module defines <glossa-overlay>; that's its purpose.
export { activate, GlossaOverlay } from "./activate.js";
export type { ActivateOptions, Overlay } from "./activate.js";
export type { OverlayHost } from "./overlay.js";
export { ApiError, OverlayApi } from "./api.js";
export type { ApiOptions, InContext, Problem, Syntax, TokenProvider } from "./api.js";
export { locate } from "./target.js";
export type { Target } from "./target.js";
