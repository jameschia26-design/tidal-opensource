// Phase 1c tax model on screen: the financial tax settings form, the chart's default-rate column and
// the document forms taking a line's rate from the account it posts to.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (p) => readFileSync(new URL(`../${p}`, import.meta.url), "utf8");

test("tax settings screen exposes every Xero financial-settings field", () => {
  const page = read("app/(app)/settings/tax/page.tsx");
  for (const label of ["Tax basis", "Tax ID number", "Tax ID display name", "Tax period", "For sales", "For purchases"]) {
    assert.match(page, new RegExp(label.replace(/ /g, " ")), `${label} shown`);
  }
  assert.match(page, /option value="accrual"/);
  assert.match(page, /option value="payment"/);
  assert.match(page, /\["monthly", "Monthly"\], \["quarterly", "Quarterly"\], \["twice_monthly", "Twice monthly"\]/);
  assert.match(page, /\/v1\/tax\/settings/);
  assert.match(page, /method: "PUT"/);
  // The catalogue is shown but not editable: rates are statutory data.
  assert.match(page, /\/v1\/tax\/rates/);
  assert.doesNotMatch(page, /New tax rate/);
});

test("chart of accounts shows and edits the default tax rate", () => {
  const page = read("app/(app)/accounting/page.tsx");
  assert.match(page, /<th>Tax rate<\/th>/);
  assert.match(page, /Default tax rate for /);
  assert.match(page, /\/v1\/accounts\/\$\{id\}/);
  assert.match(page, /tax_rate_code: code/);
  assert.match(page, /const TAX_CODES/);
  assert.match(page, /No default/);
});

test("document forms default the line rate from the chosen account", () => {
  const invoice = read("components/InvoiceForm.tsx");
  assert.match(invoice, /tax_rate_code\?: string/);
  assert.match(invoice, /function chooseAccount/);
  assert.match(invoice, /gst_code: code \|\| line\.gst_code/);
  const bills = read("app/(app)/purchases/page.tsx");
  assert.match(bills, /tax_rate_code\?: string/);
  assert.match(bills, /gst: code \|\| l\.gst/);
});

test("settings index links the tax settings screen", () => {
  const section = read("components/SettingsSection.tsx");
  assert.match(section, /href: "\/settings\/tax"/);
  assert.match(section, /single GST control account/);
  assert.match(section, /Fiscal year-end is selected when the company is created and then locked/);
  assert.match(section, /Company time zone is configurable/);
});
