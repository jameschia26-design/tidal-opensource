import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const profile = readFileSync(new URL("../app/(app)/settings/profile/page.tsx", import.meta.url), "utf8");

test("profile edits name only and explains email identity", () => {
  assert.match(profile, /method: "PATCH", body: JSON\.stringify\(\{ name: value \}\)/);
  assert.match(profile, /htmlFor="display-name"/);
  assert.match(profile, /role="alert"/);
  assert.match(profile, /role="status"/);
  assert.match(profile, /verified email identifies and links your account/);
  assert.match(profile, /Google account is separate/);
  assert.doesNotMatch(profile, /type="email"/);
});
