/**
 * Collects a build's usages module by module and turns them into the
 * `glossa.usages/v1` document.
 */
import type { SourceMapInput } from "@jridgewell/trace-mapping";

import { ModuleLocator, sourceReader, stripQuery } from "./locate.js";
import type { ReadSource } from "./locate.js";
import { scanMarkup } from "./markup.js";
import { scan } from "./scan.js";
import type { Node } from "./scan.js";
import { compileRoutes, componentOf, isGenerated, normalizeUsages, projectPath, routeOf } from "./usage.js";
import type { Routes, Usage } from "./usage.js";

export class Collector {
  /** Usages per build environment and module, so a rebuilt module replaces its old ones. */
  private readonly modules = new Map<string, Usage[]>();
  private readonly routes: Array<[string, RegExp[]]>;
  private read: ReadSource = sourceReader();

  constructor(
    public root: string,
    routes: Routes | undefined,
    public accessors: Map<string, string> = new Map(),
  ) {
    this.routes = compileRoutes(routes);
  }

  /** Forgets cached file contents (a new build, e.g. in watch mode). */
  reset(): void {
    this.read = sourceReader();
  }

  /** A transformed JS module: its code, the map back to its sources, and its AST. */
  module(scope: string, id: string, code: string, map: SourceMapInput | null | undefined, program: Node): void {
    const locator = new ModuleLocator(code, id, map, this.read);
    const out: Usage[] = [];
    for (const hit of scan(code, program, { accessors: this.accessors })) {
      const at = locator.locate(hit);
      if (!at || isGenerated(at.source.text)) continue;
      const file = projectPath(this.root, at.source.path);
      if (!file) continue;
      const usage: Usage = { key: hit.key, file, line: at.line, column: at.column, kind: hit.kind };
      const component = componentOf(file, () => {
        for (let s = hit.scope; s; s = s.parent) {
          const name = locator.name(s.offset, s.name);
          if (name && /^[A-Z]/.test(name)) return name;
        }
        return undefined;
      });
      if (component) usage.component = component;
      const route = routeOf(file, this.routes);
      if (route) usage.route = route;
      out.push(usage);
    }
    this.modules.set(`${scope}\0${id}`, out);
  }

  /** An HTML file as written (Vite's `transformIndexHtml`, before any processing). */
  html(scope: string, filename: string, html: string): void {
    const path = stripQuery(filename);
    const file = projectPath(this.root, path);
    if (!file) return;
    const out: Usage[] = [];
    const starts = [0];
    for (let i = html.indexOf("\n"); i >= 0; i = html.indexOf("\n", i + 1)) starts.push(i + 1);
    for (const hit of scanMarkup(html)) {
      let line = starts.length - 1;
      while (starts[line]! > hit.offset) line--;
      const column = Array.from(html.slice(starts[line]!, hit.offset)).length + 1;
      const usage: Usage = { key: hit.key, file, line: line + 1, column, kind: "element" };
      const route = routeOf(file, this.routes);
      if (route) usage.route = route;
      out.push(usage);
    }
    this.modules.set(`${scope}\0${path}`, out);
  }

  forget(id: string): void {
    for (const k of this.modules.keys()) if (k.endsWith(`\0${id}`)) this.modules.delete(k);
  }

  usages(): Usage[] {
    return normalizeUsages([...this.modules.values()].flat());
  }
}
