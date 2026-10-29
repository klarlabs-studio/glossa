import { describe, expect, it } from "vitest";
import { allows, allowsFor, grantFor } from "./permissions";

describe("grantFor", () => {
  it("gives owners everything, everywhere", () => {
    const g = grantFor({ roles: ["owner"], locales: [] });
    expect(allows(g, "owners.manage")).toBe(true);
    expect(allowsFor(g, "translations.review", "ja")).toBe(true);
  });

  it("keeps admins from owner changes", () => {
    const g = grantFor({ roles: ["admin"], locales: [] });
    expect(allows(g, "owners.manage")).toBe(false);
    expect(allows(g, "tenant.manage")).toBe(true);
  });

  it("lets developers write but not review", () => {
    const g = grantFor({ roles: ["developer"], locales: [] });
    expect(allows(g, "catalog.write")).toBe(true);
    expect(allowsFor(g, "translations.write", "de")).toBe(true);
    expect(allowsFor(g, "translations.review", "de")).toBe(false);
  });

  it("limits translators and reviewers to their locales and CLDR descendants", () => {
    const g = grantFor({ roles: ["reviewer"], locales: ["de", "es-419"] });
    expect(allowsFor(g, "translations.review", "de")).toBe(true);
    expect(allowsFor(g, "translations.review", "de-AT")).toBe(true);
    expect(allowsFor(g, "translations.write", "es-AR")).toBe(true);
    expect(allowsFor(g, "translations.write", "es")).toBe(false);
    expect(allowsFor(g, "translations.review", "fr")).toBe(false);
    expect(allows(g, "translations.review")).toBe(false);
    expect(allows(g, "catalog.read")).toBe(true);
    expect(allows(g, "catalog.write")).toBe(false);
  });

  it("mirrors knowledge and AI permissions", () => {
    const dev = grantFor({ roles: ["developer"], locales: [] });
    expect(allows(dev, "knowledge.write")).toBe(true);
    expect(allows(dev, "intelligence.manage")).toBe(false);
    expect(allowsFor(dev, "intelligence.translate", "ja")).toBe(true);
    expect(allows(grantFor({ roles: ["admin"], locales: [] }), "intelligence.manage")).toBe(true);
    const tr = grantFor({ roles: ["translator"], locales: ["de"] });
    expect(allows(tr, "knowledge.read")).toBe(true);
    expect(allows(tr, "knowledge.write")).toBe(false);
    expect(allows(tr, "intelligence.read")).toBe(true);
    expect(allowsFor(tr, "intelligence.translate", "de-AT")).toBe(true);
    expect(allowsFor(tr, "intelligence.translate", "fr")).toBe(false);
  });

  it("mirrors import and export permissions", () => {
    const dev = grantFor({ roles: ["developer"], locales: [] });
    expect(allows(dev, "integration.manage")).toBe(true);
    expect(allowsFor(dev, "integration.import", "ja")).toBe(true);
    const tr = grantFor({ roles: ["translator"], locales: ["de"] });
    expect(allows(tr, "integration.read")).toBe(true);
    expect(allowsFor(tr, "integration.import", "de-AT")).toBe(true);
    expect(allowsFor(tr, "integration.import", "fr")).toBe(false);
    expect(allows(tr, "integration.manage")).toBe(false);
  });

  it("doesn't let a locale scope narrow another role's permission", () => {
    const g = grantFor({ roles: ["developer", "translator"], locales: ["de"] });
    expect(allowsFor(g, "translations.write", "fr")).toBe(true);
  });

  it("unions scopes and treats a missing membership as nothing", () => {
    expect(grantFor(undefined).size).toBe(0);
    expect(allowsFor(grantFor(undefined), "translations.read", "de")).toBe(false);
  });

  it("mirrors the operations permissions (RFC 0006 §4.2): approving is a reviewer's, per locale", () => {
    const rv = grantFor({ roles: ["reviewer"], locales: ["de"] });
    expect(allowsFor(rv, "approvals.decide", "de-AT")).toBe(true);
    expect(allowsFor(rv, "approvals.decide", "fr")).toBe(false);
    expect(allows(rv, "assignments.read")).toBe(true);
    expect(allows(rv, "assignments.manage")).toBe(false);
    const tr = grantFor({ roles: ["translator"], locales: ["de"] });
    expect(allows(tr, "assignments.read")).toBe(true);
    expect(allows(tr, "workflows.read")).toBe(true);
    expect(tr.get("approvals.decide")).toBeUndefined();
    expect(grantFor({ roles: ["developer"], locales: [] }).get("approvals.decide")).toBeUndefined();
    const admin = grantFor({ roles: ["admin"], locales: [] });
    expect(allowsFor(admin, "approvals.decide", "ja")).toBe(true);
    expect(allows(admin, "audit.export")).toBe(false);
    expect(allows(grantFor({ roles: ["owner"], locales: [] }), "audit.export")).toBe(true);
  });
});
