// User-facing screens must expose the features the API already has, and must not deny them in copy.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (p) => readFileSync(new URL(`../${p}`, import.meta.url), "utf8");

test("expenses form offers GST codes", () => {
  const page = read("app/(app)/purchases/expenses/page.tsx");
  assert.match(page, /ex-gst/);
  assert.match(page, /Standard-rated \(SR\)/);
  assert.match(page, /gst_code: form\.gst/);
  assert.doesNotMatch(page, /expenses without GST/);
});

test("bank reconciliation is per account", () => {
  const page = read("app/(app)/reports/bank-reconciliation/page.tsx");
  assert.match(page, /bank_account_id=\$\{bankId\}/);
  assert.match(page, /is_bank_account/);
  assert.doesNotMatch(page, /no per-account/);
});

test("bills expose credit notes and reversal, and admit stock and asset lines", () => {
  const page = read("app/(app)/purchases/page.tsx");
  assert.match(page, /BillCorrections/);
  assert.doesNotMatch(page, /remain unsupported/);
  assert.doesNotMatch(page, /reversal is not available yet/);
  const panel = read("components/BillCorrections.tsx");
  assert.match(panel, /credit-notes/);
  assert.match(panel, /supplier-credits\/\$\{credit\.id\}\/refund/);
  assert.match(panel, /\/void/);
});

test("supplier credit editor exposes configured currency and dated rates", () => {
  const page = read("app/(app)/purchases/supplier-credits/page.tsx");
  assert.match(page, /sc-currency/);
  assert.match(page, /fx_rate_micro/);
  assert.match(page, /rate_date <= date/);
});

test("contacts support selection and bulk add to group", () => {
  const page = read("components/ContactsPage.tsx");
  assert.match(page, /Select \$\{c\.name\}/);
  assert.match(page, /addSelectedToGroup/);
  assert.match(page, /contact_ids: selectedContacts/);
});

test("assets expose disposal", () => {
  const page = read("app/(app)/accounting/assets/page.tsx");
  assert.match(page, /dispose/);
  assert.doesNotMatch(page, /Disposals, revaluation/);
});

test("integrations wording matches the limits page", () => {
  const page = read("app/(app)/settings/integrations/page.tsx");
  assert.match(page, /InvoiceNow \(peppol e-invoicing\)", status: "Not supported"/);
  assert.match(page, /Bank feeds \(live bank connection\)", status: "Not supported"/);
});
