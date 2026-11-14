import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const users = readFileSync(new URL("../app/(app)/settings/users/page.tsx", import.meta.url), "utf8");
const invitations = readFileSync(new URL("../app/(app)/invitations/page.tsx", import.meta.url), "utf8");

test("company sharing uses explicit acceptance and management endpoints", () => {
  assert.match(invitations, /api<\{ invitations: Invitation\[\] \}>\("\/v1\/invitations"\)/);
  assert.match(invitations, /\/v1\/invitations\/\$\{invitation.id\}\/accept/);
  assert.match(invitations, /await refresh\(\)/);
  assert.match(invitations, /select\(result.org_id\)/);
  assert.doesNotMatch(invitations, /org_id=|selected\?/);
  assert.match(users, /\/v1\/members\/\$\{member.id\}\?org_id=\$\{orgId\}/);
  assert.match(users, /method: "PATCH"/);
  assert.match(users, /method: "DELETE"/);
  assert.match(users, /m.role !== "owner"/);
  assert.match(users, /selected\?\.role === "owner" \|\| m.role !== "admin"/);
  assert.match(users, /v.role !== "admin" \|\| selected\?\.role === "owner"/);
  assert.doesNotMatch(users, /first time they sign in|now has access/);
});
