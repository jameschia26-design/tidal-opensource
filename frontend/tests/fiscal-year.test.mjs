import test from "node:test";
import assert from "node:assert/strict";
import { financialYearToDate, fiscalYearStart } from "../lib/fiscal.ts";

test("fiscalYearStart follows the company year end", () => {
  // December year end behaves like the calendar year.
  assert.equal(fiscalYearStart("2026-03-01", "12-31"), "2026-01-01");
  // A 30 June year end: July onwards belongs to the year that ends next 30 June.
  assert.equal(fiscalYearStart("2026-07-05", "06-30"), "2026-07-01");
  // ...and March belongs to the year that started the previous 1 July.
  assert.equal(fiscalYearStart("2026-03-01", "06-30"), "2025-07-01");
  // A 31 March year end: 1 April starts the new year.
  assert.equal(fiscalYearStart("2026-04-01", "03-31"), "2026-04-01");
  assert.equal(fiscalYearStart("2026-03-31", "03-31"), "2025-04-01");
  // Missing or malformed year end falls back to the calendar year.
  assert.equal(fiscalYearStart("2026-05-05", ""), "2026-01-01");
});

test("home widget period is the financial year to date, never a calendar year", () => {
  // 30 September year end: October starts the new year, so the period never runs past today.
  assert.deepEqual(financialYearToDate("2026-10-15", "09-30"), { from: "2026-10-01", to: "2026-10-15" });
  assert.deepEqual(financialYearToDate("2026-10-01", "09-30"), { from: "2026-10-01", to: "2026-10-01" });
  // The last day of the year still belongs to the year that started the previous October.
  assert.deepEqual(financialYearToDate("2026-09-30", "09-30"), { from: "2025-10-01", to: "2026-09-30" });
  // A December year end still ends at today, not 31 December.
  assert.deepEqual(financialYearToDate("2026-11-02", "12-31"), { from: "2026-01-01", to: "2026-11-02" });
  // Malformed year end falls back to the calendar year but keeps the today cap.
  assert.deepEqual(financialYearToDate("2026-11-02", ""), { from: "2026-01-01", to: "2026-11-02" });
});
