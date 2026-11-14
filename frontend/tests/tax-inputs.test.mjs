import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import ts from "typescript";

function load(file, imports = {}) {
  const source = readFileSync(new URL(file, import.meta.url), "utf8");
  const { outputText } = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } });
  const exports = {};
  runInNewContext(outputText, { exports, require: name => { assert.ok(name in imports); return imports[name]; } });
  return exports;
}
const money = load("../lib/money.ts");
const { parseTaxItems } = load("../lib/tax-items.ts", { "./money": money });

test("signed tax inputs preserve losses without accepting negative sales prices", () => {
  assert.equal(money.parseSignedCents("-1,234.56"), -123456);
  assert.equal(money.parseSignedCents("1200.50"), 120050);
  assert.equal(money.parseCents("-1.00"), null);
  for (const value of ["--1", "1.234", "NaN", "-", "9007199254740992"]) assert.equal(money.parseSignedCents(value), null);
});
test("formatMoneyInput adds commas while typing", () => {
  assert.equal(money.formatMoneyInput("1000"), "1,000");
  assert.equal(money.formatMoneyInput("1000000"), "1,000,000");
  assert.equal(money.formatMoneyInput("1234.5"), "1,234.5");
  assert.equal(money.formatMoneyInput("1234.56"), "1,234.56");
  assert.equal(money.formatMoneyInput("-1000.50"), "-1,000.50");
  assert.equal(money.formatMoneyInput(""), "");
});
test("tax item inputs retain labels, evidence and integer cents", () => {
  const input = { label: " srs ", amount: "1,200.50", reason: "Eligible contribution", ref: "Statement 1" };
  assert.equal(JSON.stringify(parseTaxItems([input])), JSON.stringify([{ label: "srs", amount: 120050, reason: input.reason, ref: input.ref }]));
  for (const bad of [{ ...input, label: " " }, { ...input, amount: "-1" }, { ...input, amount: "1.001" }]) assert.throws(() => parseTaxItems([bad]));
});
