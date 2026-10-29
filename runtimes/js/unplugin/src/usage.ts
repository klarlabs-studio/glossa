/**
 * The `glossa.usages/v1` document (RFC 0004 §2.2,
 * runtimes/testdata/schemas/usages.v1.schema.json): usages with their file,
 * component and route, sorted the way the fixture contract says.
 */
import { relative, sep } from "node:path";

import { compareCodePoints } from "./keys.js";

export type UsageKind = "t" | "component" | "element" | "accessor" | "template";

export interface Usage {
  key: string;
  file: string;
  line: number;
  column: number;
  component?: string;
  route?: string;
  kind: UsageKind;
}

export interface UsagesDocument {
  schema: "glossa.usages/v1";
  application: string;
  commit: string;
  branch: string;
  tool: { name: string; version: string };
  usages: Usage[];
}

/** Route pattern → file globs (`**` crosses directories, `*` doesn't), relative to the root. */
export type Routes = Record<string, string[]>;

/** A route glob as a regular expression over root-relative paths. */
export function globRegExp(glob: string): RegExp {
  let out = "";
  for (let i = 0; i < glob.length; ) {
    if (glob.startsWith("**", i)) {
      out += ".*";
      i += 2;
    } else if (glob[i] === "*") {
      out += "[^/]*";
      i++;
    } else {
      out += glob[i]!.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
      i++;
    }
  }
  return new RegExp(`^${out}$`, "u");
}

export function compileRoutes(routes: Routes | undefined): Array<[string, RegExp[]]> {
  return Object.entries(routes ?? {}).map(([route, globs]) => [route, globs.map(globRegExp)]);
}

/** Astro's `src/pages/**` route of a file, else the first `routes` entry whose glob matches it. */
export function routeOf(file: string, routes: Array<[string, RegExp[]]>): string | undefined {
  if (file.startsWith("src/pages/") && file.endsWith(".astro")) {
    const segs = file
      .slice("src/pages/".length, -".astro".length)
      .split("/")
      .filter((s) => s !== "index");
    return `/${segs.join("/")}`;
  }
  for (const [route, globs] of routes) if (globs.some((g) => g.test(file))) return route;
  return undefined;
}

const JSX = /\.[cm]?[jt]sx$/;
const SFC = /\.(?:vue|astro)$/;

/**
 * Vue and Astro: the file name without its extension. JSX/TSX: the nearest
 * enclosing capitalized function or class, found in the AST. Anything else
 * has no component.
 */
export function componentOf(file: string, jsxComponent: () => string | undefined): string | undefined {
  if (SFC.test(file)) return file.slice(file.lastIndexOf("/") + 1).split(".")[0] || undefined;
  if (JSX.test(file)) return jsxComponent();
  return undefined;
}

/** The root-relative, `/`-separated, NFC path of a file, or undefined when it's outside the root. */
export function projectPath(root: string, path: string): string | undefined {
  const rel = relative(root, path);
  if (!rel || rel.startsWith("..") || rel.split(sep).includes("node_modules")) return undefined;
  if (/^[a-zA-Z]:/.test(rel) || rel.startsWith(sep)) return undefined;
  return rel.split(sep).join("/").normalize("NFC");
}

/** `// Code generated … DO NOT EDIT.` on the first line: the accessors `glossa generate` writes. */
export function isGenerated(text: string): boolean {
  const first = text.slice(0, text.indexOf("\n") >>> 0).replace(/\r$/, "");
  return /^\/\/ Code generated .* DO NOT EDIT\.$/.test(first);
}

export function compareUsages(a: Usage, b: Usage): number {
  return (
    compareCodePoints(a.key, b.key) ||
    compareCodePoints(a.file, b.file) ||
    a.line - b.line ||
    a.column - b.column
  );
}

/** Sorted by key, file, line, column; one usage per (file, line, column). */
export function normalizeUsages(usages: Iterable<Usage>): Usage[] {
  const byPosition = new Map<string, Usage>();
  for (const u of usages) {
    const pos = `${u.file}\0${u.line}\0${u.column}`;
    if (!byPosition.has(pos)) byPosition.set(pos, u);
  }
  return [...byPosition.values()].sort(compareUsages).map((u) => {
    const out: Usage = { key: u.key, file: u.file, line: u.line, column: u.column } as Usage;
    if (u.component !== undefined) out.component = u.component;
    if (u.route !== undefined) out.route = u.route;
    out.kind = u.kind;
    return out;
  });
}
