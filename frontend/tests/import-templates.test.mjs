// Import CSV templates never drift: each template header row equals the headers its importer checks.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import ts from "typescript";

function load(file, imports = {}) {
  const source = readFileSync(new URL(file, import.meta.url), "utf8");
  const { outputText } = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } });
  const exports = {};
  runInNewContext(outputText, { exports, require: (name) => { assert.ok(name in imports, name); return imports[name]; } });
  return exports;
}
const { csvTemplate, BANK_STATEMENT_HEADERS } = load("../lib/csv.ts");
const { importSpecs } = load("../lib/imports.ts");
const specs = importSpecs({ contacts: {}, suppliers: {}, accounts: {} });

test("every import spec template header equals the spec headers it validates", () => {
  const kinds = ["accounts", "customers", "vendors", "items", "bills", "invoices", "quotes", "sales receipts", "credit notes", "payments", "expenses", "recurring expenses"];
  assert.deepEqual(Object.keys(specs).sort(), [...kinds].sort());
  for (const kind of kinds) {
    const first = csvTemplate(specs[kind].headers).split("\n")[0];
    assert.equal(first, specs[kind].headers.join(","), kind);
  }
});

test("record templates carry one ISO-date example row", () => {
  for (const spec of Object.values(specs)) {
    const lines = csvTemplate(spec.headers).split("\n");
    assert.equal(lines.length, 3, spec.label); // header, example, trailing newline
    const example = Object.fromEntries(spec.headers.map((h, i) => [h, lines[1].split(",")[i]]));
    for (const [h, v] of Object.entries(example)) {
      if (/_date$/.test(h)) assert.match(v, /^\d{4}-\d{2}-\d{2}$/, `${spec.label}.${h}`);
    }
  }
});

test("bank statement template matches the importer columns", () => {
  assert.equal([...BANK_STATEMENT_HEADERS].join(","), "date,description,reference,amount");
  const [head, example] = csvTemplate(BANK_STATEMENT_HEADERS).split("\n");
  assert.equal(head, "date,description,reference,amount");
  const cells = example.split(",");
  assert.match(cells[0], /^\d{4}-\d{2}-\d{2}$/);
  assert.ok(cells[1].length > 0 && !Number.isNaN(Number(cells[3])));
});
