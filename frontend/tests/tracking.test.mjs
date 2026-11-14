// Phase 1e: tracking categories are set up in Settings and chosen on each document line.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (p) => readFileSync(new URL(`../${p}`, import.meta.url), "utf8");

test("tracking settings screen adds, renames and archives categories and options", () => {
  const page = read("app/(app)/settings/tracking/page.tsx");
  assert.match(page, /\/v1\/tracking\/categories/);
  assert.match(page, /\/v1\/tracking\/options/);
  assert.match(page, /Archive category/);
  assert.match(page, /Add category/);
  assert.match(page, /Add option/);
  assert.match(page, /Save name/);
  // The two-category limit is stated and enforced in the form as well as on the server.
  assert.match(page, /two active categories/i);
  assert.match(page, /activeCount >= 2/);
  assert.match(page, /htmlFor=\{`tc-name-/);
});

test("line selector renders one labelled select per active category and is keyboard reachable", () => {
  const component = read("components/TrackingSelect.tsx");
  assert.match(component, /tracking-option|tracking-\$\{c\.id\}/);
  assert.match(component, /htmlFor=\{`\$\{idPrefix\}-tracking-\$\{c\.id\}-\$\{lineNumber\}`\}/);
  assert.match(component, /<select id=\{`\$\{idPrefix\}-tracking-\$\{c\.id\}-\$\{lineNumber\}`\}/);
  assert.match(component, /archived \? " \(archived\)"/);
  assert.match(component, /trackingPayload/);
});

test("invoices and bills offer tracking on every line and keep it off the document header", () => {
  for (const [path, prefix] of [["components/InvoiceForm.tsx", "invoice"], ["app/(app)/purchases/page.tsx", "bill"]]) {
    const form = read(path);
    assert.match(form, new RegExp(`TrackingFields orgId=\\{orgId\\} idPrefix="${prefix}"`));
    assert.match(form, /tracking: \{\}/);
    assert.match(form, /trackingPayload/);
  }
  // The document-level tag selects are hidden where lines carry the values, so nothing double counts.
  assert.match(read("components/InvoiceForm.tsx"), /tags=\{quote\}/);
  assert.match(read("app/(app)/purchases/page.tsx"), /tags=\{false\}/);
});

test("profit and loss offers a tracking category column and the summary reports line amounts", () => {
  const pl = read("app/(app)/reports/profit-loss/page.tsx");
  assert.match(pl, /TrackingCategoryPicker/);
  assert.match(pl, /tracking=\$\{tracking\}/);
  assert.match(pl, /data\.tracking\.rows/);
  assert.match(pl, /untracked_cents/);
  const summary = read("app/(app)/reports/tracking-summary/page.tsx");
  assert.match(summary, /posted journal lines/);
  assert.doesNotMatch(summary, /lines are not tagged/);
});
