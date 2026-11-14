// Static guard: proxy source keeps the hardening rules. Browser smoke covers behavior.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";

const upstream = readFileSync(new URL("../lib/upstream.ts", import.meta.url), "utf8");
const route = readFileSync(new URL("../app/api/[...path]/route.ts", import.meta.url), "utf8");
test("upstream allowlists http(s) origin only", () => {
  assert.match(upstream, /http:" && url\.protocol !== "https:/);
  assert.match(upstream, /origin only/);
});
test("proxy forces no-store, copies every Set-Cookie, times out", () => {
  assert.match(route, /cache-control", "no-store"/);
  assert.match(route, /getSetCookie\(\)/);
  assert.match(route, /AbortSignal\.timeout/);
});
test("client forwarding headers are never forwarded raw", () => {
  assert.match(route, /CLIENT_FORWARDING/);
  assert.match(route, /x-vercel-forwarded-for/);
  for (const h of ["x-tidal-client-ip", "x-tidal-client-ts", "x-tidal-client-sig"]) {
    assert.match(route, new RegExp(`CLIENT_FORWARDING[\\s\\S]*${h}`, "m"), `${h} must be stripped from client input`);
  }
});
test("signed client IP is set from the edge IP only on Vercel", () => {
  // First entry of x-vercel-forwarded-for, falling back to x-real-ip / x-forwarded-for.
  assert.match(route, /get\("x-real-ip"\)/);
  assert.match(route, /get\("x-forwarded-for"\)/);
  assert.match(route, /split\(","\)\[0\]\.trim\(\)/);
  assert.match(route, /process\.env\.VERCEL === "1"/);
  // No secret means no signed headers, so the backend keeps its peer-based fallback.
  assert.match(route, /process\.env\.TIDAL_PROXY_SECRET/);
  assert.match(route, /createHmac\("sha256", proxySecret\)/);
  assert.match(route, /update\(`\$\{edgeClientIp\}\|\$\{ts\}`\)/);
});
test("cohost edge IP: only Traefik-set secret plus rightmost hop counts", async () => {
  const { edgeClientIP } = await import("../lib/client-ip.ts");
  const h = (pairs) => new Headers(pairs);
  const secret = "e".repeat(32);

  // No secret configured, missing header, or wrong secret: no client IP at all.
  assert.equal(edgeClientIP(h({ "x-forwarded-for": "203.0.113.9" }), ""), "");
  assert.equal(edgeClientIP(h({ "x-forwarded-for": "203.0.113.9" }), secret), "");
  assert.equal(edgeClientIP(h({ "x-tidal-edge-secret": "x".repeat(32), "x-forwarded-for": "203.0.113.9" }), secret), "");

  // Correct secret: rightmost entry wins, so client-supplied left entries cannot spoof.
  assert.equal(edgeClientIP(h({ "x-tidal-edge-secret": secret, "x-forwarded-for": "203.0.113.9" }), secret), "203.0.113.9");
  assert.equal(edgeClientIP(h({ "x-tidal-edge-secret": secret, "x-forwarded-for": "10.0.0.1, 203.0.113.9" }), secret), "203.0.113.9");

  // IPv6, and fail-closed when the rightmost hop is not an address.
  assert.equal(edgeClientIP(h({ "x-tidal-edge-secret": secret, "x-forwarded-for": "2001:db8::1" }), secret), "2001:db8::1");
  assert.equal(edgeClientIP(h({ "x-tidal-edge-secret": secret, "x-forwarded-for": "203.0.113.9, evil" }), secret), "");
  assert.equal(edgeClientIP(h({ "x-tidal-edge-secret": secret }), secret), "");

  // The proxy route strips any client copy and signs only the verified address.
  assert.match(route, /edgeClientIP\(req\.headers, process\.env\.TIDAL_EDGE_SECRET/);
  assert.match(route, /EDGE_SECRET_HEADER/);
});
