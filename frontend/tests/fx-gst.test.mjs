// Phase 3b: currencies, FX report and GST returns are reachable and use the ledger endpoints.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { NAV } from "../lib/nav.ts";

const read = (p) => readFileSync(new URL(`../${p}`, import.meta.url), "utf8");

test("currencies page lists currencies, saves a rate and renames a currency", () => {
  const page = read("app/(app)/settings/currencies/page.tsx");
  assert.match(page, /\/v1\/currencies\?org_id=/);
  assert.match(page, /\/v1\/currencies\/rates/);
  assert.match(page, /method: "PUT"/);
  assert.match(page, /Add currency/);
  assert.match(page, /Exchange rate/);
});

test("FX report shows realised and unrealised movements and runs revaluation", () => {
  const page = read("app/(app)/reports/fx-gains-losses/page.tsx");
  assert.match(page, /\/v1\/reports\/fx-gains-losses\?org_id=/);
  assert.match(page, /\/v1\/fx\/revalue/);
  assert.match(page, /Realised/);
  assert.match(page, /Unrealised/);
  assert.match(page, /reconcile/i);
});

test("GST returns page uses the ledger F5, files a period and keeps the worksheet as a memo", () => {
  const page = read("app/(app)/tax/gst/page.tsx");
  assert.match(page, /\/v1\/tax\/gst-returns\?org_id=/);
  assert.match(page, /\/v1\/reports\/gst-f5\?org_id=/);
  assert.match(page, /\/v1\/tax\/gst-returns\/file/);
  assert.match(page, /tax\/gst\/worksheet/);
  assert.doesNotMatch(page, /Calculate worksheet/);
  const memo = read("app/(app)/tax/gst/worksheet/page.tsx");
  assert.match(memo, /memo/i);
  assert.match(memo, /\/v1\/tax\/gst-f5/);
});

test("nav exposes currencies and the FX report once each", () => {
  const hrefs = NAV.flatMap((g) => g.items.map((i) => i.href));
  assert.equal(hrefs.filter((h) => h === "/settings/currencies").length, 1);
  assert.equal(hrefs.filter((h) => h === "/reports/fx-gains-losses").length, 1);
  assert.equal(NAV.find((g) => g.label === "Tax")?.items[0].href, "/tax/gst");
});
