import assert from "node:assert/strict";
import test from "node:test";
import { reportDateRange } from "../lib/report-presets.ts";

test("financial year presets respect non-calendar year end and leap day", () => {
  assert.deepEqual(reportDateRange("fytd", "2026-11-07", "09-30"), { from: "2026-10-01", to: "2026-11-07" });
  assert.deepEqual(reportDateRange("fytd", "2026-03-31", "03-31"), { from: "2025-04-01", to: "2026-03-31" });
  assert.deepEqual(reportDateRange("last_fy", "2026-11-07", "09-30"), { from: "2025-10-01", to: "2026-09-30" });
  assert.deepEqual(reportDateRange("fytd", "2024-02-29", "02-28"), { from: "2024-02-29", to: "2024-02-29" });
});

test("rejects invalid dates and unsupported presets", () => {
  assert.throws(() => reportDateRange("fytd", "2026-02-30"), RangeError);
  assert.throws(() => reportDateRange("unknown", "2026-11-07"), RangeError);
});

test("calendar presets return expected boundaries", () => {
  assert.deepEqual(reportDateRange("this_month", "2026-11-07"), { from: "2026-11-01", to: "2026-11-07" });
  assert.deepEqual(reportDateRange("last_month", "2026-01-07"), { from: "2025-12-01", to: "2025-12-31" });
  assert.deepEqual(reportDateRange("this_quarter", "2026-11-07"), { from: "2026-10-01", to: "2026-11-07" });
  assert.deepEqual(reportDateRange("last_year", "2026-01-07"), { from: "2025-01-01", to: "2025-12-31" });
  assert.deepEqual(reportDateRange("last_quarter", "2026-01-07"), { from: "2025-10-01", to: "2025-12-31" });
  assert.deepEqual(reportDateRange("month_to_date", "2026-11-07"), { from: "2026-11-01", to: "2026-11-07" });
  assert.deepEqual(reportDateRange("quarter_to_date", "2026-11-07"), { from: "2026-10-01", to: "2026-11-07" });
});
