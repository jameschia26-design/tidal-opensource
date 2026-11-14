// Phase 1d: lifecycle tabs and editable numbering are visible in the UI.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (p) => readFileSync(new URL(`../${p}`, import.meta.url), "utf8");

test("invoice and bill lists show lifecycle tabs with counts", () => {
  for (const path of ["app/(app)/sales/invoices/page.tsx", "app/(app)/purchases/page.tsx"]) {
    const page = read(path);
    assert.match(page, /Awaiting approval/);
    assert.match(page, /Awaiting payment/);
    assert.match(page, /Voided/);
    assert.match(page, /counts\[tab\.id\]/);
  }
  assert.match(read("app/(app)/accounting/page.tsx"), /journalFilter/);
  assert.match(read("app/(app)/accounting/page.tsx"), /Void/);
});

test("numbering settings screen edits every document type", () => {
  const page = read("app/(app)/settings/numbering/page.tsx");
  for (const label of ["Invoice", "Bill", "Credit note", "Purchase order", "Quote"]) assert.match(page, new RegExp(label));
  assert.match(page, /document-numbering/);
  assert.match(page, /prefix/);
  assert.match(page, /next number/);
});

test("invoice number is editable in the form and not assigned by posting", () => {
  const form = read("components/InvoiceForm.tsx");
  assert.match(form, /document_number/);
  assert.match(form, /quote_number/);
  assert.doesNotMatch(form, /Assigned when posted/);
  assert.match(form, /setDocumentNumber\(e\.target\.value\)/);
});
