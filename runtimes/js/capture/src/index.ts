export { startCapture, stripMarkers, digest } from "./session.js";
export type { CaptureSession, LoggedRender, SessionCapture, SessionOptions } from "./session.js";
export { collectRegions } from "./regions.js";
export type { Box, Capture, CapturedRender, Host, LogEntry, Region } from "./regions.js";
// The visual probe pass itself is `@klarlabs-studio/glossa-capture/probes`, so a bundle that
// never probes doesn't carry it (RFC 0005 §5.1). Only its types are here.
export type {
  Baseline,
  ProbeContext,
  ProbeFinding,
  ProbeLocus,
  ProbeOptions,
  ProbePass,
  ProbeResult,
} from "./probes.js";
export { END, START, mark, ranges, strip } from "./markers.js";
export type { Marked } from "./markers.js";
