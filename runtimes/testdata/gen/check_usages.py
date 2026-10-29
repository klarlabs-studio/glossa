#!/usr/bin/env python3
"""Check the usage fixture suite and the shared schemas (RFC 0004 §2.1–§3.3, RFC 0005 §2.1).

Unlike the runtime fixtures, `usages/*/expected.json` are written by hand:
they are the contract that @klarlabs-studio/glossa/unplugin and `glossa extract` must both
meet. This script keeps them honest:

- the schemas — usages.v1, captures.v1 and finding.v1 — are valid JSON
  Schema 2020-12, the examples in schemas/examples/ validate, and a list of
  broken variants does not;
- every case has case.json, a project/ tree and an expected.json that
  validates against usages.v1, carries the runner header, is sorted and
  formatted canonically, and whose every position points at the key (or
  the accessor) in the source file;
- the component and route rules that can be checked mechanically hold, and
  no look-alike key (fake.*) is ever expected.

    python3 runtimes/testdata/gen/check_usages.py        # check (CI)
    python3 runtimes/testdata/gen/check_usages.py --fix  # rewrite expected.json canonically
"""

import copy
import json
import re
import sys
import unicodedata
from pathlib import Path

import jsonschema
from referencing import Registry, Resource

ROOT = Path(__file__).resolve().parents[1]
REPO = ROOT.parents[1]
SCHEMAS = ROOT / "schemas"
EXAMPLES = SCHEMAS / "examples"
CASES = ROOT / "usages"

# What a runner passes to the extractor under test; `tool` is whatever the
# implementation reports and is never compared.
HEADER = {
    "schema": "glossa.usages/v1",
    "application": "fixture",
    "commit": "0123456789abcdef0123456789abcdef01234567",
    "branch": "main",
    "tool": {"name": "fixture", "version": "0.0.0"},
}
USAGE_FIELDS = ["key", "file", "line", "column", "component", "route", "kind"]
IMPLEMENTATIONS = ["unplugin", "extract"]
LOOKALIKE = "fake."
TEMPLATE_EXTS = (".tmpl", ".gotmpl", ".gohtml")
NO_COMPONENT_EXTS = (".html", ".ts", ".js", ".mjs", ".cjs") + TEMPLATE_EXTS

CASE_SCHEMA = {
    "type": "object",
    "additionalProperties": False,
    "required": ["description", "implementations", "keys"],
    "properties": {
        "description": {"type": "string", "minLength": 1},
        "implementations": {
            "type": "array", "minItems": 1, "uniqueItems": True,
            "items": {"enum": IMPLEMENTATIONS},
        },
        "keys": {
            "type": "array", "uniqueItems": True,
            "items": {"type": "string", "pattern": r"^[a-z0-9_-]+(?:\.[a-z0-9_-]+)*$"},
        },
        "routes": {
            "type": "object",
            "propertyNames": {"pattern": "^/"},
            "additionalProperties": {"type": "array", "minItems": 1, "items": {"type": "string"}},
        },
    },
}


class Failures(list):
    def add(self, where, what):
        self.append(f"{where}: {what}")


# ── Schemas and examples ────────────────────────────────────────────────

def load(path):
    return json.loads(path.read_text(encoding="utf-8"))


def validators():
    schemas = [load(SCHEMAS / f"{n}.v1.schema.json") for n in ("usages", "captures", "finding")]
    for s in schemas:
        jsonschema.Draft202012Validator.check_schema(s)
    registry = Registry().with_resources((s["$id"], Resource.from_contents(s)) for s in schemas)
    fmt = jsonschema.Draft202012Validator.FORMAT_CHECKER
    return tuple(jsonschema.Draft202012Validator(s, registry=registry, format_checker=fmt) for s in schemas)


def mutate(doc, path, value):
    """Return a copy of doc with the value at path (a list of keys/indexes) replaced; None deletes.

    An empty path replaces the whole document, for a variant that changes
    two fields at once.
    """
    if not path:
        return copy.deepcopy(value)
    out = copy.deepcopy(doc)
    target = out
    for step in path[:-1]:
        target = target[step]
    if value is None:
        del target[path[-1]]
    else:
        target[path[-1]] = value
    return out


U = ["usages", 0]
USAGE_VARIANTS = [
    # (valid?, why, path, value)
    (True, "SHA-256 commit", ["commit"], "a" * 64),
    (True, "dotfile directory and '...' segment", U + ["file"], ".storybook/.../preview.ts"),
    (True, "scoped tool name and prerelease version", ["tool"], {"name": "@klarlabs-studio/glossa", "version": "0.1.0-rc.1+build.5"}),
    (True, "nested branch name", ["branch"], "renovate/vite-6.x"),
    (False, "short commit", ["commit"], "9f2c1e7"),
    (False, "uppercase commit", ["commit"], "A" * 40),
    (False, "branch with '..'", ["branch"], "feat/../main"),
    (False, "branch component starting with '.'", ["branch"], "feat/.hidden"),
    (False, "branch with a space", ["branch"], "my branch"),
    (False, "branch ending in '.'", ["branch"], "release."),
    (False, "application that isn't a slug", ["application"], "Web App"),
    (False, "unknown schema version", ["schema"], "glossa.usages/v2"),
    (False, "extra top-level field", ["digest"], "abc"),
    (False, "missing tool version", ["tool"], {"name": "glossa"}),
    (False, "absolute path", U + ["file"], "/src/App.vue"),
    (False, "parent segment", U + ["file"], "src/../App.vue"),
    (False, "leading parent segment", U + ["file"], "../App.vue"),
    (False, "current-directory segment", U + ["file"], "./src/App.vue"),
    (False, "backslash separators", U + ["file"], "src\\App.vue"),
    (False, "drive letter", U + ["file"], "C:/src/App.vue"),
    (False, "empty segment", U + ["file"], "src//App.vue"),
    (False, "trailing slash", U + ["file"], "src/"),
    (False, "line 0", U + ["line"], 0),
    (False, "column 0", U + ["column"], 0),
    (False, "fractional line", U + ["line"], 1.5),
    (False, "kind outside the enum", U + ["kind"], "go"),
    (False, "typed is now accessor", U + ["kind"], "typed"),
    (False, "key that isn't a message key", U + ["key"], "Checkout Pay"),
    (False, "key over 200 characters", U + ["key"], "a" * 201),
    (False, "route that is a URL", U + ["route"], "https://shop.example.com/checkout"),
    (False, "route with a query", U + ["route"], "/checkout?step=2"),
    (False, "empty component", U + ["component"], ""),
    (False, "component with spaces", U + ["component"], "PaymentFooter > PrimaryButton"),
    (False, "extra usage field", U + ["messageId"], "msg_1"),
    (False, "missing kind", U + ["kind"], None),
]

R = ["captures", 0, "regions"]
CAPTURE_VARIANTS = [
    (True, "no deviceScaleFactor", ["captures", 0, "viewport", "deviceScaleFactor"], None),
    (True, "off-screen region with a negative box origin", R + [2, "box"], {"x": -40, "y": -10, "width": 0, "height": 0}),
    (False, "no captures", ["captures"], []),
    (False, "region with both key and index", R + [0, "index"], 0),
    (False, "region with neither key nor index", R + [1, "index"], None),
    (False, "attribute region without the attribute name", R + [2, "attribute"], None),
    (False, "attribute name on a text region", R + [1, "attribute"], "title"),
    (False, "region kind outside the enum", R + [0, "kind"], "image"),
    (False, "negative width", R + [0, "box", "width"], -1),
    (False, "missing visible", R + [0, "visible"], None),
    (False, "uppercase image digest", ["captures", 0, "image", "sha256"], "A" * 64),
    (False, "route that is a URL", ["captures", 0, "route"], "http://localhost:4173/checkout"),
    (False, "url that isn't http", ["captures", 0, "url"], "file:///etc/passwd"),
    (False, "zero viewport", ["captures", 0, "viewport", "width"], 0),
    (False, "deviceScaleFactor 0", ["captures", 0, "viewport", "deviceScaleFactor"], 0),
    (False, "locale that isn't BCP 47", ["captures", 0, "locale"], "de_DE"),
    (False, "render without locale", ["captures", 0, "renders", 0, "locale"], None),
    (False, "extra capture field", ["captures", 0, "html"], "<html>"),
]


F = ["locus"]
MINIMAL_FINDING = {
    "schema": "glossa.finding/v1", "fingerprint": "f_0123456789abcdef", "layer": "completeness",
    "code": "missing-translation", "severity": "error",
    "locus": {"key": "checkout.pay", "locale": "fr"}, "message": "missing translation",
}
WAIVED_FINDING = dict(MINIMAL_FINDING, severity="waived", waiver="0192f5d3-5e6f-7081-82a3-4d5e6f708192")
FINDING_VARIANTS = [
    (True, "nothing but the required fields", [], MINIMAL_FINDING),
    (True, "a waived finding that names its waiver", [], WAIVED_FINDING),
    (True, "a kernel qualifier alongside the code", ["detail"], "number->string"),
    (True, "a terminology code, which is spelled with an underscore", ["code"], "term_forbidden"),
    (False, "a waived finding with no waiver", ["severity"], "waived"),
    (False, "an unwaived finding that names a waiver", ["waiver"], "0192f5d3-5e6f-7081-82a3-4d5e6f708192"),
    (False, "a layer nobody implements", ["layer"], "semantic"),
    (False, "a severity a layer may not emit", ["severity"], "never"),
    (False, "a code in shouting case", ["code"], "Expansion-Excessive"),
    (False, "a fingerprint that isn't one", ["fingerprint"], "7c1a"),
    (False, "an unprefixed fingerprint", ["fingerprint"], "7c1a3e5b9d024f68"),
    (False, "an unknown schema version", ["schema"], "glossa.finding/v2"),
    (False, "an extra top-level field", ["check"], "arguments"),
    (False, "an empty explanation", ["message"], ""),
    (False, "evidence that isn't an object", ["evidence"], [1.74]),
    (False, "no locus at all", ["locus"], None),
    (False, "an extra locus field", F + ["file_hash"], "abc"),
    (False, "a message ID that isn't a UUID", F + ["message"], "msg_1"),
    (False, "a line with no file", F + ["file"], None),
    (False, "a region with no capture", F + ["capture"], None),
    (False, "a locale that isn't BCP 47", F + ["locale"], "fr_FR"),
    (False, "an absolute file path", F + ["file"], "/src/App.vue"),
    (False, "a route that is a URL", F + ["route"], "https://shop.example.com/checkout"),
    (False, "line 0", F + ["line"], 0),
    (False, "a span side outside the two", F + ["span", "side"], "both"),
    (False, "a span with no end", F + ["span", "end"], None),
    (False, "a negative span start", F + ["span", "start"], -1),
    (False, "a fix kind nobody can apply", ["fix", "kind"], "rewrite"),
    (False, "a fix with no kind", ["fix", "kind"], None),
    (False, "an extra fix field", ["fix", "why"], "too long"),
    (False, "a negative source revision", ["source_revision"], -1),
]


def check_examples(v_usages, v_captures, v_finding, fail):
    usages_example = load(EXAMPLES / "usages.v1.json")
    captures_example = load(EXAMPLES / "captures.v1.json")
    finding_example = load(EXAMPLES / "finding.v1.json")
    for name, validator, doc, variants in (
        ("usages.v1.json", v_usages, usages_example, USAGE_VARIANTS),
        ("captures.v1.json", v_captures, captures_example, CAPTURE_VARIANTS),
        ("finding.v1.json", v_finding, finding_example, FINDING_VARIANTS),
    ):
        where = f"schemas/examples/{name}"
        for err in validator.iter_errors(doc):
            fail.add(where, f"{err.json_path}: {err.message}")
        for valid, why, path, value in variants:
            ok = validator.is_valid(mutate(doc, path, value))
            if ok != valid:
                fail.add(where, f"variant '{why}' should be {'valid' if valid else 'rejected'}")
    for i, capture in enumerate(captures_example["captures"]):
        indexes = [r["index"] for r in capture["renders"]]
        if len(set(indexes)) != len(indexes):
            fail.add("schemas/examples/captures.v1.json", f"captures[{i}]: duplicate render index")
        for region in capture["regions"]:
            if "index" in region and region["index"] not in indexes:
                fail.add("schemas/examples/captures.v1.json", f"captures[{i}]: region index {region['index']} has no render")


# ── Fixture cases ───────────────────────────────────────────────────────

def sort_key(u):
    # Python compares str by code point, which is the documented order.
    return (u["key"], u["file"], u["line"], u["column"])


def render(doc):
    """Canonical expected.json: fixed field order, one usage per line."""
    head = ",\n".join(f"  {json.dumps(k)}: {json.dumps(doc[k], ensure_ascii=False, separators=(', ', ': '))}"
                      for k in HEADER)
    usages = sorted(doc["usages"], key=sort_key)
    lines = []
    for u in usages:
        ordered = {k: u[k] for k in USAGE_FIELDS if k in u}
        ordered.update({k: v for k, v in u.items() if k not in ordered})
        lines.append("    " + json.dumps(ordered, ensure_ascii=False, separators=(", ", ": ")))
    body = "[\n" + ",\n".join(lines) + "\n  ]" if lines else "[]"
    return "{\n" + head + ",\n" + f'  "usages": {body}\n' + "}\n"


def glob_re(pattern):
    out = ""
    i = 0
    while i < len(pattern):
        if pattern.startswith("**", i):
            out += ".*"
            i += 2
        elif pattern[i] == "*":
            out += "[^/]*"
            i += 1
        else:
            out += re.escape(pattern[i])
            i += 1
    return re.compile(f"^{out}$")


def expected_route(file, routes):
    if file.startswith("src/pages/") and file.endswith(".astro"):
        segs = [s for s in file[len("src/pages/"):-len(".astro")].split("/") if s != "index"]
        return "/" + "/".join(segs)
    for route, globs in (routes or {}).items():
        if any(glob_re(g).match(file) for g in globs):
            return route
    return None


IDENT_CHAIN = re.compile(r"[A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*\s*\(")


def normalized(s):
    return re.sub(r"[^a-z0-9]", "", s.lower())


def check_position(where, u, lines, fail):
    if u["line"] > len(lines):
        fail.add(where, f"line {u['line']} is past the end of {u['file']}")
        return
    text = lines[u["line"] - 1]
    col = u["column"] - 1
    if col >= len(text):
        fail.add(where, f"column {u['column']} is past the end of {u['file']}:{u['line']}")
        return
    if u["kind"] == "accessor":
        m = IDENT_CHAIN.match(text, col)
        if not m or normalized(m.group(0)[:-1].strip()) != normalized(u["key"]):
            fail.add(where, f"{u['file']}:{u['line']}:{u['column']} is not an accessor call for {u['key']}")
        return
    if text[col:col + len(u["key"])] != u["key"] or col == 0 or text[col - 1] not in "\"'`":
        fail.add(where, f"{u['file']}:{u['line']}:{u['column']} doesn't start the quoted key {u['key']}")


GO_PACKAGE = re.compile(r"^package\s+(\w+)", re.M)
GO_COMPONENT = re.compile(r"^(\w+)\.(?:\w+|\w+\.\w+|\(\*\w+\)\.\w+)$")


def check_rules(where, u, routes, src, fail):
    file = u["file"]
    if file.endswith(".go") and "component" in u:
        pkg, m = GO_PACKAGE.search(src), GO_COMPONENT.match(u["component"])
        if not m or not pkg or m.group(1) != pkg.group(1):
            fail.add(where, f"{file}: component {u['component']!r} isn't pkg.Func, pkg.Type.Method or pkg.(*Type).Method of this package")
    if u["key"].startswith(LOOKALIKE):
        fail.add(where, f"{u['key']} is a look-alike and must never be reported")
    stem = file.rsplit("/", 1)[-1].split(".", 1)[0]
    if file.endswith((".vue", ".astro")) and u.get("component") != stem:
        fail.add(where, f"{file}: component must be the file name {stem!r}")
    if file.endswith(NO_COMPONENT_EXTS) and "component" in u:
        fail.add(where, f"{file}: this kind of file has no component")
    route = expected_route(file, routes)
    if u.get("route") != route:
        fail.add(where, f"{file}: route should be {route!r}, is {u.get('route')!r}")
    if file.endswith(TEMPLATE_EXTS) != (u["kind"] == "template"):
        fail.add(where, f"{file}: kind template is for Go templates, and only for them")


def check_case(case_dir, v_usages, fix, fail):
    where = f"usages/{case_dir.name}"
    case_file, expected_file, project = case_dir / "case.json", case_dir / "expected.json", case_dir / "project"
    if not case_file.is_file() or not expected_file.is_file() or not project.is_dir():
        fail.add(where, "needs case.json, expected.json and project/")
        return
    case = load(case_file)
    for err in jsonschema.Draft202012Validator(CASE_SCHEMA).iter_errors(case):
        fail.add(f"{where}/case.json", f"{err.json_path}: {err.message}")
    raw = expected_file.read_text(encoding="utf-8")
    doc = json.loads(raw)
    for err in v_usages.iter_errors(doc):
        fail.add(f"{where}/expected.json", f"{err.json_path}: {err.message}")
    for k, v in HEADER.items():
        if doc.get(k) != v:
            fail.add(f"{where}/expected.json", f"{k} must be the runner header value {v!r}")
    canonical = render(doc)
    if fix:
        if raw != canonical:
            expected_file.write_text(canonical, encoding="utf-8")
    elif raw != canonical:
        fail.add(f"{where}/expected.json", "not canonical (sorted by key, file, line, column; one usage per line): run with --fix")
    seen = set()
    for u in doc.get("usages", []):
        pos = (u.get("file"), u.get("line"), u.get("column"))
        if pos in seen:
            fail.add(f"{where}/expected.json", f"two usages at {pos}")
        seen.add(pos)
        if not all(k in u for k in ("key", "file", "line", "column", "kind")):
            continue
        if unicodedata.normalize("NFC", u["file"]) != u["file"]:
            fail.add(where, f"{u['file']}: file names are NFC")
        src = project / u["file"]
        if not src.is_file():
            fail.add(where, f"{u['file']} doesn't exist in project/")
            continue
        text = src.read_text(encoding="utf-8")
        check_position(where, u, text.split("\n"), fail)
        check_rules(where, u, case.get("routes"), text, fail)


def main():
    fix = "--fix" in sys.argv
    fail = Failures()
    v_usages, v_captures, v_finding = validators()
    check_examples(v_usages, v_captures, v_finding, fail)
    cases = sorted(p for p in CASES.iterdir() if p.is_dir())
    if not cases:
        fail.add("usages", "no fixture cases")
    for case_dir in cases:
        check_case(case_dir, v_usages, fix, fail)
    if fail:
        print("usage fixtures or context schemas are inconsistent:", *fail, sep="\n  ")
        sys.exit(1)
    print(f"{len(cases)} usage fixture cases and the context schemas are consistent")


if __name__ == "__main__":
    main()
