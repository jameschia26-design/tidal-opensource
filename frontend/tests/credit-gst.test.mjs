import { test } from "node:test";
import assert from "node:assert/strict";
import { creditGstRate, creditGstCents } from "../lib/credit-gst.ts";

test("credit GST uses date-based rates and rounds each line", () => {
  assert.equal(creditGstRate("2022-12-31"), 7);
  assert.equal(creditGstRate("2023-01-01"), 8);
  assert.equal(creditGstRate("2024-01-01"), 9);
  assert.equal(creditGstCents(10000, "SR", 9), 900);
  assert.equal(creditGstCents(5, "SR", 9) + creditGstCents(6, "SR", 9), 1);
  assert.equal(creditGstCents(10000, "NONE", 9), 0);
});
