// R28/R30/R32: one page states the deliberate limits, and signup points at it.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const page = readFileSync(new URL("../app/limits/page.tsx", import.meta.url), "utf8");
const signup = readFileSync(new URL("../app/(auth)/signup/page.tsx", import.meta.url), "utf8");
const users = readFileSync(new URL("../app/(app)/settings/users/page.tsx", import.meta.url), "utf8");
const expenses = readFileSync(new URL("../app/(app)/purchases/expenses/page.tsx", import.meta.url), "utf8");

test("limits page lists each deliberate limit", () => {
  for (const text of ["CSV", "does not send money", "One stock pool", "No employee expense claims", "SGD only", "InvoiceNow is not supported", "worksheets", "No separate approval step", "iras.gov.sg", "Latin text only", "worked example"]) {
    assert.ok(page.includes(text), `limits page missing: ${text}`);
  }
  assert.match(page, /What Tidal does not do/);
});

test("signup and the users page disclose the limits", () => {
  assert.match(signup, /SGD only/);
  assert.match(signup, /InvoiceNow is not supported/);
  assert.match(signup, /href="\/limits"/);
  assert.match(users, /no separate approval step/i);
});

test("tax pages say worksheet, not a filed return", async () => {
  const { readFileSync } = await import("node:fs");
  const cit = readFileSync(new URL("../app/(app)/tax/income/cit/page.tsx", import.meta.url), "utf8");
  const pit = readFileSync(new URL("../app/(app)/tax/income/pit/page.tsx", import.meta.url), "utf8");
  assert.match(cit, /Not a filed return/);
  assert.match(pit, /Not a filed return/);
  assert.match(pit, /IRAS worked example/);
});

test("payment screens say money is only recorded", () => {
  assert.match(expenses, /records money already paid/);
});
