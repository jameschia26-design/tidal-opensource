import assert from "node:assert/strict";
import test from "node:test";
import { readFile } from "node:fs/promises";

const page = await readFile(new URL("../app/(app)/time/projects/page.tsx", import.meta.url), "utf8");
const money = await readFile(new URL("../lib/money.ts", import.meta.url), "utf8");

test("project estimates are parsed as cents, validated, and sent on create", () => {
  assert.match(page, /const cost = estCost\.trim\(\) === "" \? 0 : parseCents\(estCost\)/);
  assert.match(page, /const charge = estCharge\.trim\(\) === "" \? 0 : parseCents\(estCharge\)/);
  assert.match(page, /cost === null \|\| charge === null/);
  assert.match(page, /estimated_cost_cents: cost, estimated_charge_cents: charge/);
  assert.match(page, /setEstCost\(""\); setEstCharge\(""\)/);
  assert.match(page, /async function saveEstimates/);
  assert.match(page, /PATCH.*estimated_cost_cents/s);
  assert.match(page, /Edit estimates/);
  assert.match(page, /Maximum two decimal places/);
  assert.match(page, /aria-describedby="project-estimate-help"/);
  assert.match(money, /Number\.isSafeInteger\(cents\)/);
});
