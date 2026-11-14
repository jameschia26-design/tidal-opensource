// The profit and loss and balance sheet screens must render Xero's sections and computed lines in
// order, and the CSV export reads the same rendered rows, so these helpers carry both.
import { test } from "node:test";
import assert from "node:assert/strict";
import { bsBlocks, pnlBlocks } from "../lib/report-shape.ts";
import { csvCell } from "../lib/csv.ts";

const pnlSections = [
  { key: "revenue", label: "Revenue", total_cents: 157000, lines: [
    { code: "4110", name: "Sales", class: "sales", amount_cents: 127000 },
    { code: "8110", name: "Service revenue", class: "revenue", amount_cents: 30000 },
  ] },
  { key: "cost_of_sales", label: "Cost of sales", total_cents: 30000, lines: [
    { code: "5210", name: "Freight", class: "cost_of_sales", amount_cents: 30000 },
  ] },
  { key: "operating_expenses", label: "Operating expenses", total_cents: 20000, lines: [
    { code: "6510", name: "Rent", class: "expense", amount_cents: 20000 },
  ] },
  { key: "other_income", label: "Other income", total_cents: 10000, lines: [
    { code: "8210", name: "Interest income", class: "other_income", amount_cents: 10000 },
  ] },
  { key: "other_expense", label: "Other expense", total_cents: 4000, lines: [
    { code: "6910", name: "Donations", class: "other_expense", amount_cents: 4000 },
  ] },
];
const pnl = {
  revenue_cents: 157000, cost_of_sales_cents: 30000, gross_profit_cents: 127000,
  operating_expenses_cents: 20000, operating_profit_cents: 107000,
  other_income_cents: 10000, other_expense_cents: 4000, expense_cents: 50000,
  profit_cents: 113000, sections: pnlSections,
};
const previousPnl = {
  ...pnl, revenue_cents: 100000, cost_of_sales_cents: 20000, gross_profit_cents: 80000,
  operating_expenses_cents: 15000, operating_profit_cents: 65000,
  other_income_cents: 5000, other_expense_cents: 1000, expense_cents: 35000, profit_cents: 69000,
  sections: pnlSections.map((s) => ({ ...s, total_cents: 0, lines: [] })),
};

const bsSections = [
  { key: "bank", label: "Bank", total_cents: 100000, lines: [{ code: "2810", name: "Savings", class: "bank", amount_cents: 100000 }] },
  { key: "current_assets", label: "Current assets", total_cents: 10000, lines: [{ code: "1210", name: "Receivables", class: "current_asset", amount_cents: 10000 }] },
  { key: "fixed_assets", label: "Fixed assets", total_cents: 20000, lines: [{ code: "1710", name: "Plant", class: "fixed_asset", amount_cents: 20000 }] },
  { key: "inventory", label: "Inventory", total_cents: 5000, lines: [{ code: "2410", name: "Stock", class: "inventory", amount_cents: 5000 }] },
  { key: "prepayments", label: "Prepayments", total_cents: 2000, lines: [{ code: "1410", name: "Prepaid rent", class: "prepayment", amount_cents: 2000 }] },
  { key: "non_current_assets", label: "Non-current assets", total_cents: 3000, lines: [{ code: "1610", name: "Goodwill", class: "non_current_asset", amount_cents: 3000 }] },
  { key: "current_liabilities", label: "Current liabilities", total_cents: 9000, lines: [{ code: "2210", name: "Trade payables", class: "current_liability", amount_cents: 9000 }] },
  { key: "non_current_liabilities", label: "Non-current liabilities", total_cents: 6000, lines: [{ code: "2510", name: "Term loan", class: "non_current_liability", amount_cents: 6000 }] },
  { key: "equity", label: "Equity", total_cents: 125000, lines: [
    { code: "3960", name: "Share capital", class: "equity", amount_cents: 7000 },
    { code: "3961", name: "Retained earnings", class: "retained_earnings", amount_cents: 5000 },
    { code: "", name: "Accumulated profit (not yet closed to retained earnings)", class: "retained_earnings", amount_cents: 113000, computed: true },
  ] },
];
const bs = { assets_cents: 140000, liabilities_cents: 15000, equity_cents: 125000, net_assets_cents: 125000, balanced: true, sections: bsSections };

// mirrors what the report toolbar exports: every rendered row of every table, in order.
function csvRows(blocks) {
  const lines = [];
  for (const block of blocks) {
    if (block.kind === "section") {
      lines.push("Code,Account,Amount");
      for (const line of block.lines) lines.push([line.code, line.name, line.amount_cents].map(csvCell).join(","));
      lines.push([block.totalLabel, "", block.total_cents].map(csvCell).join(","));
    } else {
      lines.push([block.label, "", block.amount_cents].map(csvCell).join(","));
    }
    lines.push("");
  }
  return lines;
}

test("profit and loss blocks follow Xero's order and carry the computed lines", () => {
  const blocks = pnlBlocks(pnl, previousPnl);
  assert.deepEqual(blocks.map((b) => b.label), [
    "Revenue", "Cost of sales", "Gross profit", "Operating expenses", "Operating profit",
    "Other income", "Other expense", "Net profit",
  ]);
  assert.deepEqual(blocks.map((b) => b.kind), [
    "section", "section", "total", "section", "total", "section", "section", "total",
  ]);
  assert.equal(blocks[2].amount_cents, 127000);
  assert.equal(blocks[4].amount_cents, 107000);
  assert.equal(blocks[7].amount_cents, 113000);
  assert.equal(blocks[0].totalLabel, "Total revenue");
  assert.equal(blocks[2].previous_cents, 80000);
  assert.equal(blocks[7].previous_cents, 69000);
  assert.equal(blocks[0].lines.length, 2);
});

test("balance sheet blocks group every class and end on net assets", () => {
  const blocks = bsBlocks(bs);
  assert.deepEqual(blocks.map((b) => b.label), [
    "Bank", "Current assets", "Fixed assets", "Inventory", "Prepayments", "Non-current assets",
    "Total assets", "Current liabilities", "Non-current liabilities", "Total liabilities",
    "Equity", "Net assets",
  ]);
  assert.equal(blocks.find((b) => b.label === "Net assets").amount_cents, 125000);
  const equity = blocks.find((b) => b.label === "Equity");
  assert.equal(equity.total_cents, 125000);
  // Retained earnings is its own line; the calculated profit is a separate line, counted once.
  const retained = equity.lines.filter((l) => l.class === "retained_earnings");
  assert.equal(retained.length, 2);
  assert.equal(retained.find((l) => l.computed).amount_cents, 113000);
  assert.equal(equity.lines.reduce((sum, l) => sum + l.amount_cents, 0), equity.total_cents);
});

test("CSV export rows carry the section headings, totals and computed lines", () => {
  const rows = csvRows(pnlBlocks(pnl));
  assert.equal(rows[0], "Code,Account,Amount");
  assert.ok(rows.includes("4110,Sales,127000"));
  assert.ok(rows.includes("Total revenue,,157000"));
  assert.ok(rows.includes("Gross profit,,127000"));
  assert.ok(rows.includes("Operating profit,,107000"));
  assert.ok(rows.includes("Net profit,,113000"));

  const bsRows = csvRows(bsBlocks(bs));
  assert.ok(bsRows.includes("Total assets,,140000"));
  assert.ok(bsRows.includes("Total liabilities,,15000"));
  assert.ok(bsRows.includes(",Accumulated profit (not yet closed to retained earnings),113000"));
  assert.ok(bsRows.includes("Net assets,,125000"));
});
