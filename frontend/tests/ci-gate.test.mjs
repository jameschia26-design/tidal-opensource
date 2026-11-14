// The CI gate must fail the job on a failing test or a skipped test, not just on a SKIP line
// that happens to be greppable. Regression for a `tee`-swallowed exit status.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { execFileSync } from "node:child_process";

const ci = readFileSync(new URL("../../.github/workflows/ci.yml", import.meta.url), "utf8");

test("CI test step cannot hide a failure behind tee", () => {
  assert.match(ci, /set -o pipefail/);
  assert.match(ci, /bash \.\/check-test-log\.sh \/tmp\/go-test\.log/);
  const gate = readFileSync(new URL("../../backend/check-test-log.sh", import.meta.url), "utf8");
  assert.match(gate, /FAIL\|SKIP/);
  // The dangerous shape is the single-line step where tee is the last command in the pipeline.
  assert.doesNotMatch(ci, /- run: go test .*\| tee/);
});

test("bash gate exits non-zero when the test command fails", () => {
  const script = [
    "set -o pipefail",
    "false | tee /tmp/ci-gate-probe.log",
    `bash ${new URL("../../backend/check-test-log.sh", import.meta.url).pathname} /tmp/ci-gate-probe.log`,
  ].join("\n");
  let code = 0;
  try {
    execFileSync("bash", ["-e", "-c", script], { stdio: "ignore" });
  } catch (err) {
    code = err.status;
  }
  assert.notEqual(code, 0);
});
