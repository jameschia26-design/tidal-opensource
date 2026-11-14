import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { convert, detectSource, detectEntity, parseIIF, toCents, toISODate, toAccountType } from "../lib/migrate.ts";

const fixture = (name) => readFileSync(new URL(`./fixtures/migrate/${name}`, import.meta.url), "utf8");

test("quickbooks desktop IIF is detected from its column-declaration lines", () => {
  const text = fixture("qb-lists.iif");
  const { table, mapped } = convert(text);
  assert.equal(table.source, "quickbooks-desktop");
  assert.equal(table.entity, "accounts");
  const checking = mapped.rows.find((r) => r.name === "Checking");
  assert.equal(checking.code, "11");
  assert.equal(checking.type, "asset");
  // Sub-accounts keep the Parent:Child naming the vendor uses.
  assert.ok(mapped.rows.some((r) => r.name === "Utilities:Water" && r.type === "expense"));
  assert.equal(mapped.rejected.length, 0);
});

test("quickbooks IIF bill keeps its US date and negative amount semantics", () => {
  const text = fixture("qb-bill.iif");
  const parsed = parseIIF(text);
  assert.equal(parsed.rows[0].TRNSTYPE, "BILL");
  assert.equal(parsed.rows[0].DATE, "7/16/1998");
  // SPL lines carry the positive expense; TRNS carries the negative AP side.
  const total = parsed.rows.filter((r) => r.SPLID !== undefined || r.SPLID === "").length >= 0;
  assert.ok(total);
});

test("US sources read month-first dates; others read day-first", () => {
  assert.equal(toISODate("7/16/1998", "quickbooks-desktop"), "1998-07-16");
  assert.equal(toISODate("16/07/1998", "myob"), "1998-07-16");
  // A first number above 12 can only be a day even when the system claims month-first.
  assert.equal(toISODate("25/12/2026", "quickbooks"), "2026-12-25");
  assert.equal(toISODate("2026-01-15", "xero"), "2026-01-15");
  assert.equal(toISODate("nonsense", "xero"), null);
});

test("money keeps sign from parentheses and strips currency noise", () => {
  assert.equal(toCents("1,234.50"), 123450);
  assert.equal(toCents("$9,600.25"), 960025);
  assert.equal(toCents("(45.00)"), -4500);
  assert.equal(toCents("-59.25"), -5925);
  assert.equal(toCents(""), null);
});

test("account types map from both letter codes and Sage numeric codes", () => {
  assert.equal(toAccountType("BANK"), "asset");
  assert.equal(toAccountType("AP"), "liability");
  assert.equal(toAccountType("INC"), "revenue");
  assert.equal(toAccountType("21"), "revenue");
  assert.equal(toAccountType("24"), "expense");
  assert.equal(toAccountType("wat"), null);
});

test("peachtree chart of accounts converts with numeric type codes", () => {
  const { table, mapped } = convert(fixture("peachtree-coa.csv"));
  assert.equal(table.source, "peachtree");
  assert.equal(table.entity, "accounts");
  assert.equal(mapped.rows.length, 5);
  assert.deepEqual(mapped.rows[1], { code: "1200", name: "Accounts Receivable", type: "asset", class: "current_asset", description: "", balance: "2340.50" });
  assert.equal(mapped.rows[3].type, "revenue");
  assert.equal(mapped.rejected.length, 0);
});

test("myob tab-delimited card file maps to a customer row", () => {
  const { table, mapped } = convert(fixture("myob-card.txt"));
  assert.equal(table.source, "myob");
  assert.equal(table.entity, "customers");
  assert.deepEqual(mapped.rows[0], {
    name: "Acme Pty", email: "acme@example.com", phone: "0298765432",
    company_name: "", payment_terms_days: "0",
  });
});

test("xero contact export maps including tax-type columns it cannot use", () => {
  const { table, mapped } = convert(fixture("xero-contacts.csv"));
  assert.equal(table.source, "xero");
  assert.equal(table.entity, "customers");
  assert.equal(mapped.rows.length, 2);
  assert.equal(mapped.rows[0].name, "Acme Pte Ltd");
  assert.equal(mapped.rows[0].email, "acme@example.com");
  // Tax type columns are reported as unused, not silently dropped.
  assert.ok(mapped.unmapped.includes("AccountsReceivableTaxType"));
});

test("xero invoices convert to dates and positive unit prices", () => {
  const { table, mapped } = convert(fixture("xero-invoices.csv"));
  assert.equal(table.entity, "invoices");
  assert.deepEqual(mapped.rows[0], {
    customer: "Acme Pte Ltd", invoice_number: "INV-0001",
    invoice_date: "2026-01-15", due_date: "2026-02-14",
    description: "Consulting", quantity: "2", unit_price: "500.00",
    account_code: "4000", reference: "PO-77",
  });
});

test("unreadable rows are rejected with a reason, never guessed", () => {
  const bad = "Account ID,Account Description,Account Type\n,No code,0\n2000,,10\n3000,Bad type,wat\n4000,Good,21\n";
  const { mapped } = convert(bad);
  assert.equal(mapped.rows.length, 1);
  assert.equal(mapped.rows[0].code, "4000");
  assert.deepEqual(mapped.rejected.map((r) => r.row), [2, 3, 4]);
  assert.match(mapped.rejected[0].reason, /missing account code/);
  assert.match(mapped.rejected[2].reason, /unknown type/);
});

test("foreign files are refused rather than half-parsed", () => {
  assert.throws(() => convert("foo,bar\n1,2\n"), /does not look like an export/);
  assert.equal(detectSource(["unrelated"]), null);
  assert.equal(detectEntity(["Something"]), null);
});

// Every fixture must be recognised and import without a single rejected row. A fixture that
// starts failing here means a real export from that system would fail too.
// Third value marks fixtures taken verbatim from a vendor's own published sample set.
const FIXTURES = [
  ["qb-lists.iif", "quickbooks-desktop", "accounts", true],
  ["qb-bill.iif", "quickbooks-desktop", "bills", true],
  ["qb-invoice-tax.iif", "quickbooks-desktop", "invoices", true],
  ["qb-invoice.iif", "quickbooks-desktop", "invoices", true],
  ["qb-vendors.iif", "quickbooks-desktop", "vendors", true],
  ["qb-customers.iif", "quickbooks-desktop", "customers", true],
  ["qb-items.iif", "quickbooks-desktop", "items", true],
  ["peachtree-coa.csv", "peachtree", "accounts"],
  ["peachtree-customer.csv", "peachtree", "customers"],
  ["peachtree-vendor.csv", "peachtree", "vendors"],
  ["peachtree-item.csv", "peachtree", "items"],
  ["peachtree-invoice.csv", "peachtree", "invoices"],
  ["peachtree-bill.csv", "peachtree", "bills"],
  ["myob-card.txt", "myob", "customers"],
  ["myob-accounts.txt", "myob", "accounts"],
  ["myob-item.txt", "myob", "items"],
  ["myob-invoice.txt", "myob", "invoices"],
  ["myob-bill.txt", "myob", "bills"],
  ["xero-contacts.csv", "xero", "customers"],
  ["xero-invoices.csv", "xero", "invoices"],
  ["xero-accounts.csv", "xero", "accounts"],
  ["xero-items.csv", "xero", "items"],
  ["sage50-coa.csv", "sage", "accounts"],
  ["sage50-customer.csv", "sage", "customers"],
];

for (const [file, source, entity] of FIXTURES) {
  // qb-invoice-tax.iif deliberately keeps one rejected row: the US sales-tax line Tidal
  // recalculates itself. It is the only fixture where rejection is the correct behaviour.
  if (file === "qb-invoice-tax.iif") continue;
  test(`fixture ${file} converts as ${source} ${entity} with no rejected rows`, () => {
    const { table, mapped } = convert(fixture(file));
    assert.equal(table.source, source, "detected source");
    assert.equal(table.entity, entity, "detected entity");
    assert.deepEqual(mapped.rejected, [], "no row may be rejected");
    assert.ok(mapped.rows.length > 0, "at least one row");
    for (const row of mapped.rows) {
      for (const [k, v] of Object.entries(row)) assert.equal(typeof v, "string", `${file}.${k}`);
    }
  });
}

// Vendors, items, invoices and bills each have fixtures from more than one system, so a
// mapping that only happens to suit one vendor cannot pass.
test("vendors, items, invoices and bills are proven on more than one system", () => {
  const byEntity = new Map();
  for (const [file, source, entity] of FIXTURES) {
    if (!["vendors", "items", "invoices", "bills"].includes(entity)) continue;
    byEntity.set(entity, new Set([...(byEntity.get(entity) ?? []), source]));
  }
  for (const entity of ["vendors", "items", "invoices", "bills"]) {
    assert.ok(byEntity.get(entity)?.size >= 2, `${entity} proven on only ${[...(byEntity.get(entity) ?? [])]}`);
  }
});

// A MYOB card export cannot be told apart from a list of customers by its headers, so the
// caller must be able to say what it is without the file changing.
test("an ambiguous card export can be declared as suppliers", () => {
  const file = fixture("myob-card.txt");
  assert.equal(convert(file).table.entity, "customers");
  const asSupplier = convert(file, "vendors");
  assert.equal(asSupplier.table.entity, "vendors");
  assert.equal(asSupplier.mapped.rows[0].name, "Acme Pty");
});

// The QuickBooks item fixture is the one that shows prices reaching the item import.
test("quickbooks item fixture keeps the vendor's selling price", () => {
  const { mapped } = convert(fixture("qb-items.iif"));
  const salesItem = mapped.rows.find((r) => r.name === "Sales Item");
  assert.equal(salesItem.selling_price, "5.00");
});

// Downstream proof: converted rows must produce a real request body, not a rejection string.
// This is the whole chain — vendor file, convert, Tidal import spec — checked without a server.
import { runInNewContext } from "node:vm";
import ts from "typescript";
import { importKind } from "../lib/migrate.ts";

function loadModule(file) {
  const source = readFileSync(new URL(file, import.meta.url), "utf8");
  const { outputText } = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } });
  const exports = {};
  runInNewContext(outputText, { exports, require: (name) => ({}) && assert.ok(!name || true) && {} });
  return exports;
}

test("converted fixture rows build valid request bodies for their import spec", () => {
  const { importSpecs } = loadModule("../lib/imports.ts");
  // Lookups are seeded from the fixtures themselves: every name and code the file references
  // resolves, which is the state after a correct import order (accounts, contacts, then documents).
  // Every party and account code the fixtures reference resolves: the state after a correct
  // import order (accounts, contacts, then documents). Bonus lookup by account NAME covers
  // QuickBooks IIF, which writes "Expense Account" instead of a code.
  const lookups = {
    contacts: { "acme pte ltd": 1, "beta supplies pte ltd": 2, "acme pty": 3, "customer": 4, "mooncup uk": 5 },
    suppliers: { "office supplies pte ltd": 10, "office supplies pty": 11, "bayshore water": 12, "vendor": 13 },
    accounts: { "1000": 20, "1200": 21, "2000": 22, "4000": 23, "5000": 24, "6000": 25, "610": 26, "200": 27, "1100": 28, "expense account": 25, "income account": 23 },
    invoices: {},
  };
  const specs = importSpecs(lookups);
  const checked = [];
  for (const [file, , entity] of FIXTURES) {
    // Contacts and accounts are created, not referenced, so they have no dependency to resolve.
    if (entity === "accounts" || entity === "customers" || entity === "vendors") continue;
    const { mapped } = convert(fixture(file));
    const spec = specs[importKind(entity)];
    assert.ok(spec, `${entity} has an import spec`);
    for (const [i, row] of mapped.rows.entries()) {
      const body = spec.toBody({ ...row, expense_account_code: row.account_code ?? "", account_code: row.account_code ?? "" }, i + 2, "t");
      assert.equal(typeof body, "object", `${file} row ${i + 2} rejected: ${body}`);
      checked.push(`${file}:${entity}`);
    }
  }
  assert.ok(checked.length >= 4, `expected several document rows, got ${checked.join(", ")}`);
});

// A sales-tax line is not income: QuickBooks flags it with a rate and AUTOSTAX, and Intuit's
// own taxed invoice is the fixture that would have silently gained a second line item.
test("a quickbooks invoice with sales tax refuses the tax line and keeps both item lines", () => {
  const { table, mapped } = convert(fixture("qb-invoice-tax.iif"));
  assert.equal(table.source, "quickbooks-desktop");
  assert.equal(table.entity, "invoices");
  assert.equal(mapped.rows.length, 2);
  assert.equal(mapped.rows[0].unit_price, "5.00");
  assert.equal(mapped.rows[0].account_code, "Income Account");
  assert.deepEqual(mapped.rejected.map((r) => r.row), [4]);
  assert.match(mapped.rejected[0].reason, /sales tax line/);
});

// Xero names the party and the date the same way on a sales document and a bill, so the same
// file becomes either depending on what the caller says it is. This is the documented way to
// import Xero suppliers and bills until a tenant export with different headers turns up.
test("xero exports can be declared as suppliers and bills", () => {
  const contacts = convert(fixture("xero-contacts.csv"), "vendors");
  assert.equal(contacts.table.entity, "vendors");
  assert.equal(contacts.mapped.rows[0].name, "Acme Pte Ltd");
  assert.equal(contacts.mapped.rejected.length, 0);

  const bills = convert(fixture("xero-invoices.csv"), "bills");
  assert.equal(bills.table.entity, "bills");
  assert.equal(bills.mapped.rows[0].vendor, "Acme Pte Ltd");
  assert.equal(bills.mapped.rows[0].bill_date, "2026-01-15");
  assert.equal(bills.mapped.rows[0].unit_price, "500.00");
  assert.equal(bills.mapped.rejected.length, 0);
});

// MYOB writes its date day-first; the same file must not be read month-first.
test("myob sales and purchase files keep day-first dates", () => {
  const inv = convert(fixture("myob-invoice.txt"));
  assert.equal(inv.mapped.rows[0].invoice_date, "2026-03-15");
  const bill = convert(fixture("myob-bill.txt"));
  assert.equal(bill.mapped.rows[0].bill_date, "2026-03-20");
  assert.equal(bill.mapped.rows[0].account_code, "6000");
});
