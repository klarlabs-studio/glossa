/**
 * Drawing a workflow chart with Mermaid, loaded only when a chart is
 * first drawn: Mermaid is large, and only the workflow editor needs it,
 * so it must never reach the bundle every screen pays for.
 *
 * Mermaid runs with `securityLevel: "strict"`: the chart's labels are
 * the author's text, so they are sanitized and click handlers are off.
 * Screens take the renderer by injection, so component tests draw with
 * a stand-in and never load Mermaid.
 */
import { inject, type InjectionKey } from "vue";

/** SVG markup for a Mermaid source; `id` is unique per drawing. */
export type RenderChart = (id: string, source: string) => Promise<string>;

let configured: string | undefined;

export const renderWithMermaid: RenderChart = async (id, source) => {
  const { default: mermaid } = await import("mermaid");
  const theme = document.documentElement.getAttribute("data-theme") === "dark" ? "dark" : "neutral";
  if (configured !== theme) {
    mermaid.initialize({ startOnLoad: false, securityLevel: "strict", theme, fontFamily: "inherit" });
    configured = theme;
  }
  const { svg } = await mermaid.render(id, source);
  return svg;
};

export const CHART_RENDERER: InjectionKey<RenderChart> = Symbol("chart-renderer");

export function useChartRenderer(): RenderChart {
  return inject(CHART_RENDERER, renderWithMermaid);
}
