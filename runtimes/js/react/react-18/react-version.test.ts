// Guards the aliases in vitest.config.ts: the suite must really run on React 18.3.
import { expect, it } from "vitest";
import { version } from "react";
import { version as dom } from "react-dom";
import { version as server } from "react-dom/server";

it("runs @klarlabs-studio/glossa-react's tests on React 18.3", () => {
  expect([version, dom, server]).toEqual(["18.3.1", "18.3.1", "18.3.1"]);
});
