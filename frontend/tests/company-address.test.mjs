import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import ts from "typescript";

function load(file) {
  const source = readFileSync(new URL(file, import.meta.url), "utf8");
  const { outputText } = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } });
  const exports = {};
  runInNewContext(outputText, { exports, require: () => { throw new Error("no imports"); } });
  return exports;
}
const { sameCompanyAddress } = load("../lib/company-address.ts");

const registered = { attention: "", line1: "10 Orchard Road", line2: "", city: "Singapore", region: "", postal_code: "238801", country: "SG" };

test("same-address detection ignores key order but not field values", () => {
  const reordered = { country: "SG", postal_code: "238801", region: "", city: "Singapore", line2: "", line1: "10 Orchard Road", attention: "" };
  assert.equal(sameCompanyAddress(registered, reordered), true);
  for (const change of [{ line2: "#02-01" }, { postal_code: "" }, { country: "sg" }, { attention: "Accounts" }]) {
    assert.equal(sameCompanyAddress(registered, { ...registered, ...change }), false);
  }
});
