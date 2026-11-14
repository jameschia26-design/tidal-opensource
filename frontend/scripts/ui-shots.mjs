// Capture the main screens at 1440 and 390 for before/after comparison.
//   A11Y_SESSION_TOKEN=... A11Y_ORG_ID=1 npm run shots -- /tmp/ui-shots/part2
import { chromium } from "playwright";
import { mkdir } from "node:fs/promises";
import { ROUTES } from "./a11y-check.mjs";

const BASE = process.env.A11Y_BASE_URL ?? "http://127.0.0.1:3100";
const OUT = process.argv[2] ?? "/tmp/ui-shots/part2";
const TOKEN = process.env.A11Y_SESSION_TOKEN ?? "";
const ORG_ID = process.env.A11Y_ORG_ID ?? "";

await mkdir(OUT, { recursive: true });
const browser = await chromium.launch({ executablePath: process.env.A11Y_CHROME || undefined, args: ["--no-sandbox", "--disable-dev-shm-usage"] });
const context = await browser.newContext();
if (TOKEN) {
  const url = new URL(BASE);
  await context.addCookies([{ name: "tidal_session", value: TOKEN, domain: url.hostname, path: "/" }]);
  if (ORG_ID) await context.addInitScript((id) => window.localStorage.setItem("tidal.selectedOrg", id), ORG_ID);
}
for (const route of ROUTES) {
  if (route.auth && !TOKEN) continue;
  const name = route.path === "/" ? "home" : route.path.replace(/^\//, "").replace(/\//g, "-");
  for (const width of [1440, 390]) {
    const page = await context.newPage();
    await page.setViewportSize({ width, height: width === 390 ? 844 : 1000 });
    await page.goto(`${BASE}${route.path}`, { waitUntil: "networkidle", timeout: 30_000 });
    await page.waitForTimeout(500);
    await page.screenshot({ path: `${OUT}/${name}-${width}.png`, fullPage: true });
    await page.close();
    console.log(`${name}-${width}.png`);
  }
}
await browser.close();
