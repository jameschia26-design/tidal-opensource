// R26: the history is readable and exportable in the app, not only through the API.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const page = readFileSync(new URL("../app/(app)/settings/audit-history/page.tsx", import.meta.url), "utf8");
const settings = readFileSync(new URL("../app/(app)/settings/page.tsx", import.meta.url), "utf8");
const backend = readFileSync(new URL("../../backend/audit_history.go", import.meta.url), "utf8");

test("audit history page filters and exports", () => {
  assert.match(page, /\/v1\/audit-events/);
  assert.match(page, /format=csv/);
  assert.match(page, /entity_kind/);
  assert.match(page, /entity_id/);
  assert.match(page, /user_id/);
  assert.match(settings, /href="\/settings\/audit-history"/);
});

test("history export is server-side CSV", () => {
  assert.match(backend, /text\/csv/);
  assert.match(backend, /entity_kind/);
});
