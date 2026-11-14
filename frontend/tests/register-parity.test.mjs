// Register rows 1243/1201/641/1699/4051/1347: recurring multi-line + due day, list
// import/export, tax-rate usage, credit-note projects.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (p) => readFileSync(new URL(`../${p}`, import.meta.url), "utf8");

test("recurring form posts a lines array with add/remove and an optional due day", () => {
  const page = read("app/(app)/sales/recurring/page.tsx");
  assert.match(page, /formLines/);
  assert.match(page, /Add line/);
  assert.match(page, /Remove line/);
  assert.match(page, /lines: parsed/);
  assert.match(page, /due_day/);
  assert.match(page, /Due day/);
  assert.match(page, /Blank.*term days|term days/i);
});

test("invoice and item lists link import and export the loaded rows as CSV", () => {
  const invoices = read("app/(app)/sales/invoices/page.tsx");
  assert.match(invoices, /href="\/import"/);
  assert.match(invoices, /downloadCsv\("invoices\.csv"/);
  assert.match(invoices, /csvCell/);
  const items = read("app/(app)/items/page.tsx");
  assert.match(items, /href="\/import"/);
  assert.match(items, /downloadCsv\("items\.csv"/);
  assert.match(items, /csvCell/);
});

test("tax settings groups accounts under each catalogue rate", () => {
  const page = read("app/(app)/settings/tax/page.tsx");
  assert.match(page, /\/v1\/accounts/);
  assert.match(page, /Accounts using this rate/);
  assert.match(page, /tax_rate_code === r\.code/);
});

test("credit-note lines carry a project select", () => {
  const page = read("app/(app)/sales/credit-notes/page.tsx");
  assert.match(page, /ProjectIdSelect/);
  assert.match(page, /project_id/);
  assert.match(page, /cn-project-/);
});
