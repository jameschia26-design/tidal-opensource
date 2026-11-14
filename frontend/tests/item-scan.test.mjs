import { test } from "node:test";
import { strict as assert } from "node:assert";
import { scanItem } from "../lib/item-scan.ts";

test("scanner matches one nonempty SKU without confusing case or whitespace", () => {
  const items = [{ sku: "ABC-123" }, { sku: "" }, { sku: "DUP" }, { sku: "dup" }];
  assert.equal(scanItem(items, " abc-123 "), items[0]);
  assert.equal(scanItem(items, ""), null);
  assert.equal(scanItem(items, "missing"), null);
  assert.equal(scanItem(items, "dup"), null);
});
