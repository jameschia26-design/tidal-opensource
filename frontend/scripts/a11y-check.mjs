// Accessibility gate: loads a fixed list of routes in the running dev server with Playwright and
// asserts the five things the redesign promised: one h1, no heading-level skips, every field has an
// accessible name, every image has alt text, no horizontal scroll at 390px, and text tokens that
// reach 4.5:1 against the surfaces they sit on.
//
// Run against a dev server that already has demo data:
//   A11Y_SESSION_TOKEN=... A11Y_ORG_ID=1 npm run a11y
// Set A11Y_CHROME=/path/to/chrome to use a local browser when the Playwright download is unavailable.
// Routes that need a signed-in reader are skipped (and reported as skipped) when no token is given.
import { chromium } from "playwright";

const BASE = process.env.A11Y_BASE_URL ?? "http://127.0.0.1:3100";
const TOKEN = process.env.A11Y_SESSION_TOKEN ?? "";
const ORG_ID = process.env.A11Y_ORG_ID ?? "";

export const ROUTES = [
  { path: "/", auth: false },
  { path: "/overview", auth: true },
  { path: "/accounting/snapshot", auth: true },
  { path: "/sales/invoices", auth: true },
  { path: "/sales/invoices/new", auth: true },
  { path: "/sales/credit-notes", auth: true },
  { path: "/sales/statements", auth: true },
  { path: "/sales/customers", auth: true },
  { path: "/purchases", auth: true },
  { path: "/contacts", auth: true },
  { path: "/items", auth: true },
  { path: "/reports/profit-loss", auth: true },
  { path: "/reports/balance-sheet", auth: true },
  { path: "/settings/tax", auth: true },
];

// --- pure helpers, also covered by tests/a11y-script.test.mjs ---
export function parseRgb(value) {
  const m = String(value).match(/rgba?\(\s*([\d.]+)[,\s]+([\d.]+)[,\s]+([\d.]+)/i);
  return m ? [Number(m[1]), Number(m[2]), Number(m[3])] : null;
}

export function luminance([r, g, b]) {
  const f = (c) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
}

export function contrastRatio(a, b) {
  const la = luminance(parseRgb(a));
  const lb = luminance(parseRgb(b));
  const [hi, lo] = la > lb ? [la, lb] : [lb, la];
  return (hi + 0.05) / (lo + 0.05);
}

// Heading levels in document order: the first must be 1 and no step may jump by more than one.
export function headingProblems(levels) {
  const problems = [];
  if (levels.length === 0) problems.push("no h1");
  else if (levels[0] !== 1) problems.push(`first heading is h${levels[0]}, expected h1`);
  if (levels.filter((l) => l === 1).length > 1) problems.push("more than one h1");
  for (let i = 1; i < levels.length; i++) {
    if (levels[i] > levels[i - 1] + 1) problems.push(`h${levels[i - 1]} followed by h${levels[i]}`);
  }
  return [...new Set(problems)];
}

// Runs in the page: gathers the raw facts the checks need.
function inspect() {
  const style = getComputedStyle(document.documentElement);
  const token = (name) => {
    const raw = style.getPropertyValue(name).trim();
    const probe = document.createElement("span");
    probe.style.color = raw;
    document.body.appendChild(probe);
    const value = getComputedStyle(probe).color;
    probe.remove();
    return value;
  };
  const nameOf = (field) => {
    if (field.getAttribute("aria-label")) return field.getAttribute("aria-label").trim();
    const labelledBy = field.getAttribute("aria-labelledby");
    if (labelledBy) {
      const text = labelledBy.split(/\s+/).map((id) => document.getElementById(id)?.textContent ?? "").join(" ").trim();
      if (text) return text;
    }
    if (field.id) {
      const label = document.querySelector(`label[for="${CSS.escape(field.id)}"]`);
      if (label?.textContent.trim()) return label.textContent.trim();
    }
    const wrapped = field.closest("label");
    if (wrapped?.textContent.trim()) return wrapped.textContent.trim();
    return (field.getAttribute("title") ?? "").trim();
  };
  // Hidden controls (native date pickers mirror the visible field) are out of the accessibility tree.
  const fields = [...document.querySelectorAll("input, select, textarea")].filter(
    (f) => !["hidden", "submit", "button", "reset"].includes(f.type) && f.getAttribute("aria-hidden") !== "true",
  );
  return {
    headings: [...document.querySelectorAll("h1, h2, h3, h4, h5, h6")].map((h) => Number(h.tagName[1])),
    unnamed: fields.filter((f) => !nameOf(f)).map((f) => f.id || f.name || f.outerHTML.slice(0, 60)),
    noAlt: [...document.images].filter((img) => !img.hasAttribute("alt")).map((img) => img.getAttribute("src") ?? "(inline)"),
    scrollWidth: Math.max(document.documentElement.scrollWidth, document.body.scrollWidth),
    viewport: window.innerWidth,
    tokens: {
      ink: token("--color-ink"),
      muted: token("--color-ink-muted"),
      surface: token("--color-surface"),
      surfaceAlt: token("--color-surface-alt"),
      brand: token("--color-brand"),
    },
  };
}

// 4.5:1 for the pairs the app actually renders text in.
export function tokenContrastProblems(tokens) {
  const pairs = [
    ["ink on surface", tokens.ink, tokens.surface],
    ["muted on surface", tokens.muted, tokens.surface],
    ["muted on surface-alt", tokens.muted, tokens.surfaceAlt],
    ["ink on surface-alt", tokens.ink, tokens.surfaceAlt],
  ];
  return pairs
    .map(([label, fg, bg]) => ({ label, ratio: contrastRatio(fg, bg) }))
    .filter((p) => !(p.ratio >= 4.5))
    .map((p) => `${p.label} = ${p.ratio.toFixed(2)}:1`);
}

async function checkRoute(browser, route, context, width) {
  const page = await context.newPage();
  await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
  const failures = [];
  try {
    const res = await page.goto(`${BASE}${route.path}`, { waitUntil: "networkidle", timeout: 30_000 });
    if (!res || res.status() >= 400) failures.push(`HTTP ${res?.status() ?? "no response"}`);
    await page.waitForTimeout(300);
    const facts = await page.evaluate(inspect);
    if (width === 390) {
      if (facts.scrollWidth > facts.viewport + 1) failures.push(`horizontal scroll ${facts.scrollWidth}px > ${facts.viewport}px`);
    } else {
      for (const p of headingProblems(facts.headings)) failures.push(p);
      for (const f of facts.unnamed) failures.push(`field without accessible name: ${f}`);
      for (const img of facts.noAlt) failures.push(`image without alt: ${img}`);
      for (const t of tokenContrastProblems(facts.tokens)) failures.push(`contrast ${t}`);
    }
    return failures;
  } catch (err) {
    return [`load failed: ${err.message.split("\n")[0]}`];
  } finally {
    await page.close();
  }
}

async function main() {
  const browser = await chromium.launch({
    executablePath: process.env.A11Y_CHROME || undefined,
    args: ["--no-sandbox", "--disable-dev-shm-usage"],
  });
  const context = await browser.newContext();
  if (TOKEN) {
    const url = new URL(BASE);
    await context.addCookies([{ name: "tidal_session", value: TOKEN, domain: url.hostname, path: "/" }]);
    if (ORG_ID) await context.addInitScript((id) => window.localStorage.setItem("tidal.selectedOrg", id), ORG_ID);
  }
  let failed = 0;
  let skipped = 0;
  for (const route of ROUTES) {
    if (route.auth && !TOKEN) {
      skipped++;
      console.log(`SKIP ${route.path} — needs sign-in; set A11Y_SESSION_TOKEN (and A11Y_ORG_ID)`);
      continue;
    }
    const problems = [
      ...(await checkRoute(browser, route, context, 1440)),
      ...(await checkRoute(browser, route, context, 390)),
    ];
    if (problems.length === 0) {
      console.log(`PASS ${route.path}`);
    } else {
      failed++;
      console.log(`FAIL ${route.path}`);
      for (const p of problems) console.log(`     - ${p}`);
    }
  }
  await browser.close();
  console.log(`\n${ROUTES.length - skipped - failed}/${ROUTES.length - skipped} routes pass, ${skipped} skipped`);
  process.exit(failed === 0 ? 0 : 1);
}

if (process.argv[1] && import.meta.url === `file://${process.argv[1]}`) {
  await main();
}
