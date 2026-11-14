// The accessibility gate is a real browser check; its pure parts are testable here.
import { test } from "node:test";
import assert from "node:assert/strict";
import { headingProblems, contrastRatio, ROUTES, tokenContrastProblems } from "../scripts/a11y-check.mjs";
import { readFileSync } from "node:fs";

const read = (path) => readFileSync(new URL(`../${path}`, import.meta.url), "utf8");

test("heading checks catch a missing, repeated or skipped h1", () => {
  assert.deepEqual(headingProblems([1, 2, 3]), []);
  assert.deepEqual(headingProblems([1, 2, 2, 3]), []);
  assert.deepEqual(headingProblems([]), ["no h1"]);
  assert.deepEqual(headingProblems([2, 3]), ["first heading is h2, expected h1"]);
  assert.deepEqual(headingProblems([1, 2, 1]), ["more than one h1"]);
  assert.deepEqual(headingProblems([1, 3]), ["h1 followed by h3"]);
});

test("contrast maths follows WCAG relative luminance", () => {
  assert.equal(contrastRatio("rgb(0, 0, 0)", "rgb(255, 255, 255)").toFixed(2), "21.00");
  assert.equal(contrastRatio("rgb(255, 255, 255)", "rgb(255, 255, 255)").toFixed(2), "1.00");
  assert.ok(contrastRatio("rgb(17, 38, 34)", "rgb(255, 255, 255)") >= 4.5);
  assert.equal(contrastRatio("rgb(0, 0, 0)", "rgb(0, 0, 0)").toFixed(2), "1.00");
  assert.deepEqual(tokenContrastProblems({
    ink: "rgb(17, 38, 34)", muted: "rgb(74, 92, 86)",
    surface: "rgb(255, 255, 255)", surfaceAlt: "rgb(244, 246, 242)",
  }), []);
  assert.equal(tokenContrastProblems({ ink: "rgb(220, 220, 220)", muted: "rgb(220, 220, 220)", surface: "rgb(255, 255, 255)", surfaceAlt: "rgb(255, 255, 255)" }).length, 4);
});

test("the gate loads the agreed routes and checks all five rules", () => {
  const paths = ROUTES.map((r) => r.path);
  for (const want of ["/", "/overview", "/accounting/snapshot", "/sales/invoices", "/sales/invoices/new", "/purchases", "/contacts", "/items", "/reports/profit-loss", "/reports/balance-sheet", "/settings/tax"]) {
    assert.ok(paths.includes(want), `missing route ${want}`);
  }
  const script = read("scripts/a11y-check.mjs");
  assert.match(script, /getAttribute\("aria-label"\)/);
  assert.match(script, /hasAttribute\("alt"\)/);
  assert.match(script, /scrollWidth > facts\.viewport/);
  assert.match(script, /tokenContrastProblems/);
  assert.match(script, /waitForTimeout/);
  assert.match(script, /width === 390 \? 844 : 900/);
});

test("brand green is never used as text on a dark surface", () => {
  const css = read("app/globals.css");
  const dark = css.match(/prefers-color-scheme: dark[\s\S]*?\n  \}\n\}/)[0];
  assert.match(dark, /--color-brand-text: #9fe0c5/);
  assert.ok(contrastRatio("rgb(159, 224, 197)", "rgb(13, 23, 20)") >= 7);
  assert.ok(contrastRatio("rgb(255, 255, 255)", "rgb(27, 78, 66)") >= 4.5);
  const app = read("app/globals-app.css");
  assert.doesNotMatch(app.replace(/background:\s*var\(--color-brand\)/g, ""), /(?<!background:\s?)color:\s*var\(--color-brand\)/);
});
