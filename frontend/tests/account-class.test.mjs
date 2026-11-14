// Account classes: the Xero chart category under each broad type must match the backend rules and
// the chart tabs must group the same way Xero's chart does.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { ACCOUNT_CLASSES, CLASS_TABS, classGroup, classInTab, classLabel, classesFor, defaultAccountClass } from "../lib/account-classes.ts";
import { convert } from "../lib/migrate.ts";

const fixture = (name) => readFileSync(new URL(`./fixtures/migrate/${name}`, import.meta.url), "utf8");

test("class catalogue is Xero's 16 classes with Xero's labels", () => {
  assert.deepEqual(
    ACCOUNT_CLASSES.map(c => c.label),
    ["Bank", "Current Asset", "Fixed Asset", "Inventory", "Prepayment", "Non-current Asset",
      "Current Liability", "Non-current Liability", "Equity", "Retained Earnings",
      "Sales", "Revenue", "Other Income", "Cost of Sales", "Expense", "Other Expense"],
  );
  assert.equal(new Set(ACCOUNT_CLASSES.map(c => c.value)).size, 16);
});

test("each class sits in exactly one broad type group", () => {
  const expected = {
    bank: "asset", current_asset: "asset", fixed_asset: "asset", inventory: "asset",
    prepayment: "asset", non_current_asset: "asset",
    current_liability: "liability", non_current_liability: "liability",
    equity: "equity", retained_earnings: "equity",
    sales: "revenue", revenue: "revenue", other_income: "revenue",
    cost_of_sales: "expense", expense: "expense", other_expense: "expense",
  };
  for (const [value, group] of Object.entries(expected)) {
    assert.equal(classGroup(value), group, value);
    assert.equal(classesFor(group).some(c => c.value === value), true, `${value} offered for ${group}`);
  }
  assert.equal(classGroup("banking"), null);
  assert.equal(classLabel("retained_earnings"), "Retained Earnings");
});

test("default class follows the documented code-prefix rules", () => {
  for (const [type, code, want] of [
    ["revenue", "4000", "sales"], ["revenue", "9000", "revenue"],
    ["expense", "5100", "cost_of_sales"], ["expense", "6100", "expense"],
    ["asset", "2800", "bank"], ["asset", "1500", "fixed_asset"], ["asset", "1700", "fixed_asset"],
    ["asset", "2400", "inventory"], ["asset", "1000", "current_asset"],
    ["liability", "2300", "current_liability"], ["equity", "3900", "equity"],
  ]) {
    assert.equal(defaultAccountClass(type, code), want, `${type} ${code}`);
  }
});

test("chart tabs match Xero's order and group every class", () => {
  assert.deepEqual(CLASS_TABS.map(t => t.label), ["All", "Assets", "Liabilities", "Equity", "Expenses", "Revenue"]);
  for (const c of ACCOUNT_CLASSES) {
    assert.equal(classInTab(c.value, "all"), true, c.value);
    const tab = CLASS_TABS.find(t => t.id !== "all" && classInTab(c.value, t.id));
    assert.ok(tab, `${c.value} appears in one tab`);
    assert.equal(tab.groups.length, 1, c.value);
    assert.equal(tab.groups[0], classGroup(c.value), c.value);
  }
  assert.equal(classInTab("bank", "assets"), true);
  assert.equal(classInTab("prepayment", "assets"), true);
  assert.equal(classInTab("retained_earnings", "equity"), true);
  assert.equal(classInTab("other_income", "revenue"), true);
  assert.equal(classInTab("cost_of_sales", "expenses"), true);
  assert.equal(classInTab("sales", "expenses"), false);
});

test("converted account rows carry a class alongside the broad type", () => {
  const { mapped } = convert(fixture("qb-lists.iif"));
  assert.ok(mapped.rows.length > 0);
  for (const row of mapped.rows) {
    assert.equal(row.class, defaultAccountClass(row.type, row.code), `${row.code} ${row.type}`);
  }
});
