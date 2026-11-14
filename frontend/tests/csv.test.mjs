// CSV preview must fail where the Go importer fails, so a file is never accepted in the browser and
// rejected by the server.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { runInNewContext } from "node:vm";
import ts from "typescript";

const source = readFileSync(new URL("../lib/csv.ts", import.meta.url), "utf8");
const { outputText } = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS } });
const exports = {};
runInNewContext(outputText, { exports, require: (name) => { throw new Error(name); } });
const { parseCsv, selfCheckCsv, csvCell } = exports;

test("parseCsv reads quotes, doubled quotes and CRLF", () => {
  // Compare via JSON: the parser runs in its own VM context, so its arrays are a different realm.
  assert.equal(
    JSON.stringify(parseCsv('name,email\r\n"Acme, Pte",a@x.sg\n"say ""hi""",\n\n')),
    JSON.stringify([["name", "email"], ["Acme, Pte", "a@x.sg"], ['say "hi"', ""]]),
  );
  assert.equal(selfCheckCsv(), true);
});

test("parseCsv rejects an unterminated quote with its row number", () => {
  assert.throws(() => parseCsv('name,email\n"open,cell\nnext,row\n'), /Unterminated quote starting on row 2/);
  assert.throws(() => parseCsv('a,b\nc,"still open\n'), /Unterminated quote starting on row 2/);
});

test("parseCsv rejects a stray quote inside an unquoted cell", () => {
  assert.throws(() => parseCsv('name\nAc"me\n'), /Bad quote on row 2/);
});

test("exported text cannot be read as a formula", () => {
  // R20: a leading formula character is neutralised, so no export opens as an executable cell.
  assert.equal(csvCell("=1+1"), "'=1+1");
  assert.equal(csvCell("@SUM(A1)"), "'@SUM(A1)");
  assert.equal(csvCell("+1"), "'+1");
  assert.equal(csvCell("-cmd"), "'-cmd");
  // Numbers and ordinary text stay as they are, so amount columns remain numeric.
  assert.equal(csvCell("-12.50"), "-12.50");
  assert.equal(csvCell("1234"), "1234");
  assert.equal(csvCell("Acme, Pte"), '"Acme, Pte"');
  assert.equal(csvCell('say "hi"'), '"say ""hi"""');
});
