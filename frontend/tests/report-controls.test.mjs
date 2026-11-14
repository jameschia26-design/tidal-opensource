import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";
import { reportDateRange } from "../lib/report-presets.ts";

const tools = await readFile(new URL("../components/ReportTools.tsx", import.meta.url), "utf8");
const index = await readFile(new URL("../app/(app)/reports/page.tsx", import.meta.url), "utf8");
const orgProvider = await readFile(new URL("../components/OrgProvider.tsx", import.meta.url), "utf8");
const users = await readFile(new URL("../app/(app)/settings/users/page.tsx", import.meta.url), "utf8");

test("saved reports support persisted favourites, unique-id loading, publishing and archiving", () => {
  assert.match(tools, /favourite: boolean/);
  assert.match(tools, /async function toggleFavourite/);
  assert.match(tools, /favourite \? "★" : "☆"/);
  assert.match(tools, /function applyPreset\(id: string\)/);
  assert.match(tools, /String\(p\.id\) === id/);
  assert.match(tools, /value=\{p\.id\}/);
  assert.match(tools, /Load saved report/);
  assert.match(tools, /Publish/);
  assert.match(tools, /Archive…/);
  assert.match(tools, /Financial year to date/);
  assert.match(tools, /last_fy/);
  assert.match(tools, /reportDateRange\(preset/);
  assert.match(tools, /const orgFiscalYearEnd = selected\?\.id === orgId \? selected\.fiscal_year_end/);
  assert.match(orgProvider, /fiscal_year_end\?: string/);
  assert.match(tools, /last_quarter/);
  assert.match(tools, /month_to_date/);
  assert.match(tools, /fields\.length === 1 \? to : from/);
  assert.deepEqual(reportDateRange("fytd", "2026-11-07", "09-30"), { from: "2026-10-01", to: "2026-11-07" });
});

test("user page exposes actual permission controls without contradictory copy", () => {
  assert.match(users, /FEATURES\.map/);
  assert.match(users, /aria-label=\{`\$\{label\} for \$\{m\.email\}`\}/);
  assert.doesNotMatch(users, /there is no per-feature permissions matrix/);
});

test("reports index searches report names and lists saved favourites", () => {
  assert.match(index, /Search reports/);
  assert.match(index, /r\.title\.toLocaleLowerCase\(\)\.includes\(term\)/);
  const labels = [...index.matchAll(/title: "([^"]+)", href: "\/reports\//g)].map(m => m[1]);
  assert.ok(labels.length >= 40, `expected all reports indexed, got ${labels.length}`);
  for (const label of ["Account Summary", "Aged Payables Detail", "GST Audit (Beta)", "General Ledger Detail", "Trial Balance by Date Range"]) assert.ok(labels.includes(label), `missing searchable Xero report: ${label}`);
  for (const label of ["Backorder History", "Open Backorders", "Blank Report"]) assert.ok(labels.includes(label), `missing Xero report link: ${label}`);
  assert.ok(!index.includes("<span className=\"badge\">Partial</span>"), "remove misleading partial badges from index");
  assert.match(index, /Favourites/);
  assert.match(index, /r\.favourite && r\.status !== "archived"/);
});
