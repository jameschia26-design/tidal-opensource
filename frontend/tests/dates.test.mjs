import test from "node:test";
import assert from "node:assert/strict";
import { dateOnlyBound, formatDate, isDateOnly, parseDMY, addDaysISO, todayISO, autoSlashDate } from "../lib/dates.ts";

test("autoSlashDate handles typing, backspace, and paste", () => {
  assert.equal(autoSlashDate("1", ""), "1");
  assert.equal(autoSlashDate("15", "1"), "15/");
  assert.equal(autoSlashDate("15/0", "15/"), "15/0");
  assert.equal(autoSlashDate("15/04", "15/0"), "15/04/");
  assert.equal(autoSlashDate("15/04/2", "15/04/"), "15/04/2");
  assert.equal(autoSlashDate("15", "15/"), "15", "backspace does not re-add slash");
  assert.equal(autoSlashDate("15042025", ""), "15/04/2025", "paste 8 digits");
  assert.equal(autoSlashDate("2025-04-15", ""), "15/04/2025", "paste ISO");
});
test("formatDate turns ISO into dd/mm/yyyy", () => {
  assert.equal(formatDate("2025-03-07"), "07/03/2025");
  assert.equal(formatDate("2025-12-31"), "31/12/2025");
  assert.equal(formatDate(""), "");
  assert.equal(formatDate(null), "");
});

test("parseDMY accepts real dates and pads", () => {
  assert.equal(parseDMY("07/03/2025"), "2025-03-07");
  assert.equal(parseDMY("7/3/2025"), "2025-03-07");
  assert.equal(parseDMY(" 31/12/2025 "), "2025-12-31");
  assert.equal(parseDMY("29/02/2024"), "2024-02-29", "leap day");
});

test("parseDMY rejects impossible or malformed text", () => {
  assert.equal(parseDMY("31/02/2025"), null, "February has no 31st");
  assert.equal(parseDMY("29/02/2025"), null, "2025 is not a leap year");
  assert.equal(parseDMY("01/13/2025"), null, "month 13");
  assert.equal(parseDMY("00/01/2025"), null, "day 0");
  assert.equal(parseDMY("2025-03-07"), null, "ISO is not the typed format");
  assert.equal(parseDMY("7/3/25"), null, "two-digit year");
  assert.equal(parseDMY(""), null);
});

test("parseDMY and formatDate round-trip", () => {
  for (const iso of ["2024-02-29", "2025-01-01", "2030-11-30"]) {
    assert.equal(parseDMY(formatDate(iso)), iso);
  }
});

test("addDaysISO crosses month and year ends", () => {
  assert.equal(addDaysISO("2025-01-31", 1), "2025-02-01");
  assert.equal(addDaysISO("2024-12-31", 1), "2025-01-01");
  assert.equal(addDaysISO("2024-03-01", -1), "2024-02-29");
  assert.equal(addDaysISO("not-a-date", 1), "");
});

test("todayISO is a date at Singapore time", () => {
  const today = todayISO();
  assert.match(today, /^\d{4}-\d{2}-\d{2}$/);
  const expected = new Intl.DateTimeFormat("en-CA", { timeZone: "Asia/Singapore", year: "numeric", month: "2-digit", day: "2-digit" }).format(new Date());
  assert.equal(today, expected);
});

test("formatDate never truncates a timestamp", () => {
  const stamp = "2025-03-07T10:30:00Z";
  assert.equal(formatDate(stamp), stamp, "a datetime is passed through, not cut to a day");
  assert.equal(isDateOnly(stamp), false);
  assert.equal(isDateOnly("2025-03-07"), true);
  assert.equal(isDateOnly(""), false);
  assert.equal(isDateOnly(null), false);
});

test("dateOnlyBound rejects timestamps so min/max comparison stays valid", () => {
  assert.equal(dateOnlyBound("2025-03-07"), "2025-03-07");
  assert.equal(dateOnlyBound("2025-03-07T10:30:00Z"), undefined, "a timestamp is not a date bound");
  assert.equal(dateOnlyBound("07/03/2025"), undefined);
  assert.equal(dateOnlyBound("not-a-date"), undefined);
  assert.equal(dateOnlyBound(""), undefined);
  assert.equal(dateOnlyBound(undefined), undefined);
});
