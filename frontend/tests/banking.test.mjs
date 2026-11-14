// Phase 3a: bank accounts, per-account statement import, reconcile screen and rule actions.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const read = (p) => readFileSync(new URL(`../${p}`, import.meta.url), "utf8");

test("bank accounts screen adds and edits the account record", () => {
  const page = read("app/(app)/banking/accounts/page.tsx");
  assert.match(page, /\/v1\/bank\/accounts/);
  assert.match(page, /opening_balance_cents/);
  assert.match(page, /opening_balance_date/);
  assert.match(page, /statement_balance_cents/);
  assert.match(page, /account_number/);
  assert.match(page, /account_type/);
  assert.match(page, /archived/);
  assert.match(page, /Balance in Tidal/);
  assert.match(page, /\/banking\/reconcile\?account=/);
});

test("statement import belongs to one bank account and maps its columns", () => {
  const page = read("app/(app)/banking/page.tsx");
  assert.match(page, /\/v1\/bank\/accounts/);
  assert.match(page, /bank_account_id=\$\{bankId\}/);
  assert.match(page, /col-date|cols\.date/);
  assert.match(page, /col-description|cols\.description/);
  assert.match(page, /col-reference|cols\.reference/);
  assert.match(page, /col-amount|cols\.amount/);
  assert.match(page, /const map = \[/);
});

test("reconcile screen shows suggestions, creates transactions and saves only at zero", () => {
  const page = read("app/(app)/banking/reconcile/page.tsx");
  assert.match(page, /suggestions/);
  assert.match(page, /\/v1\/bank\/lines\/\$\{line\.id\}\/match/);
  assert.match(page, /\/v1\/bank\/lines\/\$\{line\.id\}\/create/);
  assert.match(page, /difference_cents !== 0 \|\| figures\.unmatched_lines > 0/);
  assert.match(page, /\/v1\/bank\/reconciliations/);
  assert.match(page, /Spend money/);
  assert.match(page, /Receive money/);
  assert.match(page, /Transfer money/);
});

test("bank rules offer transfer, any/all conditions and contact, tax and tracking actions", () => {
  const page = read("app/(app)/banking/rules/page.tsx");
  assert.match(page, /value="transfer"/);
  assert.match(page, /value="all"/);
  assert.match(page, /value="any"/);
  assert.match(page, /rule-contact/);
  assert.match(page, /rule-tax/);
  assert.match(page, /TrackingFields/);
  assert.match(page, /suggestion only|never posted automatically/i);
});

test("chart of accounts and navigation link the banking screens", () => {
  assert.match(read("app/(app)/accounting/page.tsx"), /\/banking\/reconcile\?account=\$\{a\.id\}/);
  assert.match(read("app/(app)/accounting/page.tsx"), /href="\/banking\/accounts"/);
  const nav = read("lib/nav.ts");
  assert.match(nav, /\/banking\/accounts/);
  assert.match(nav, /\/banking\/reconcile/);
});
