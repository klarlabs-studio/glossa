/** The chart beside the workflow document: drawn for the eye, a table for everyone, never blank on a failure. */
import { flushPromises, mount } from "@vue/test-utils";
import { describe, expect, it } from "vitest";
import { CHART_RENDERER, type RenderChart } from "../../lib/mermaid";
import { reviewDocument } from "../../test/fake-workflows";
import StateChart from "./StateChart.vue";

const mountWith = (render: RenderChart, doc = reviewDocument()) =>
  mount(StateChart, { props: { doc, delay: 0 }, global: { provide: { [CHART_RENDERER as symbol]: render } } });

describe("StateChart", () => {
  it("draws the document and lists every transition as a table", async () => {
    const w = mountWith(async () => "<svg><g /></svg>");
    await flushPromises();
    expect(w.get("[data-testid=chart-svg]").html()).toContain("<svg>");
    expect(w.findAll("[data-testid=chart-table] tbody tr")).toHaveLength(4);
    expect(w.get("figure").attributes("aria-labelledby")).toBe("wf-chart-caption");
    expect(w.get("[data-testid=chart-table] caption").text()).toContain("Every transition");
  });

  it("keeps the table when the drawing fails, and says so", async () => {
    const w = mountWith(async () => {
      throw new Error("Parse error on line 3");
    });
    await flushPromises();
    expect(w.get("[data-testid=chart-failed]").text()).toContain("The chart couldn't be drawn.");
    expect(w.get("[data-testid=chart-failed]").text()).toContain("Parse error on line 3");
    expect(w.findAll("[data-testid=chart-table] tbody tr")).toHaveLength(4);
  });

  it("draws nothing for a chart without states, and says why", async () => {
    let calls = 0;
    const w = mountWith(async () => {
      calls++;
      return "<svg />";
    }, { schema: "glossa.workflow/v1" });
    await flushPromises();
    expect(w.get("[data-testid=chart-empty]").text()).toBe("Nothing to draw yet: the chart has no states.");
    expect(calls).toBe(0);
  });

  it("marks an older drawing as out of date while the text doesn't parse", async () => {
    const w = mount(StateChart, { props: { doc: reviewDocument(), stale: true, delay: 0 }, global: { provide: { [CHART_RENDERER as symbol]: async () => "<svg />" } } });
    await flushPromises();
    expect(w.get("[data-testid=chart-stale]").text()).toBe("Out of date: the text doesn't parse");
  });
});
