import { describe, expect, it } from "vitest";

import { openapiValidator } from "./testing/contract.js";
import { FakeApi, PROJECT, TENANT, TOKEN } from "./testing/fake-api.js";
import { seed } from "./testing/release.js";

describe("the fake API's contract check", () => {
  const validate = openapiValidator();

  it("rejects bodies that don't match platform/api/openapi.yaml", () => {
    expect(validate("Translation", { text: "x" }).length).toBeGreaterThan(0);
    expect(validate("NoSuchSchema", {})).toEqual(["no schema NoSuchSchema in openapi.yaml"]);
  });

  it("finds nothing to object to in what the fake serves", async () => {
    const fake = new FakeApi(validate);
    seed(fake);
    const base = `https://api.glossa.test/v1/tenants/${TENANT}/projects/${PROJECT}`;
    const headers = { Authorization: `Bearer ${TOKEN}` };
    for (const url of [
      `${base}/messages/checkout.pay`,
      `${base}/messages/checkout.pay/translations/de`,
      `${base}/messages/checkout.pay/translations/de/revisions`,
      `${base}/messages/nope`,
    ]) {
      await fake.fetch(url, { headers });
    }
    expect((await fake.fetch(`${base}/messages/checkout.pay`)).status).toBe(401);
    expect(fake.violations).toEqual([]);
  });
});
