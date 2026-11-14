import { createHmac } from "node:crypto";

import { edgeClientIP, EDGE_SECRET_HEADER } from "@/lib/client-ip";
import { backendBase, UpstreamConfigError } from "@/lib/upstream";

// Same-origin catchall: /api/* -> BACKEND_URL/api/*. Path is taken from the
// request, but only ever appended under the fixed upstream origin.
export const dynamic = "force-dynamic";

const UPSTREAM_TIMEOUT_MS = 15_000;
const HOP = new Set([
  "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
  "te", "trailer", "transfer-encoding", "upgrade", "host", "content-length",
]);

// Client-supplied forwarding headers are never passed through. Only the address
// Vercel sets at the edge is forwarded; the backend must ignore these headers
// unless the request comes from this frontend origin. The signed x-tidal-* trio
// below is set fresh here, so any client-supplied copy is stripped first.
const CLIENT_FORWARDING = new Set([
  "x-forwarded-for", "x-forwarded-host", "x-forwarded-proto", "x-forwarded-port",
  "x-real-ip", "forwarded", "x-vercel-forwarded-for",
  "x-tidal-client-ip", "x-tidal-client-ts", "x-tidal-client-sig",
  EDGE_SECRET_HEADER,
]);

function json(status: number, error: string) {
  return Response.json({ error }, { status, headers: { "cache-control": "no-store" } });
}

async function proxy(req: Request, ctx: { params: Promise<{ path: string[] }> }) {
  let base: URL;
  try {
    base = backendBase();
  } catch (err) {
    if (err instanceof UpstreamConfigError) return json(503, "backend_not_configured");
    return json(500, "internal_error");
  }

  const { path } = await ctx.params;
  const incoming = new URL(req.url);
  const target = new URL(`/api/${path.map(encodeURIComponent).join("/")}`, base);
  target.search = incoming.search;

  const headers = new Headers();
  req.headers.forEach((v, k) => {
    if (!HOP.has(k) && !CLIENT_FORWARDING.has(k)) headers.set(k, v);
  });
  // On Vercel the edge sets x-vercel-forwarded-for; fall back to the edge-set
  // x-real-ip / x-forwarded-for. Off Vercel (local dev) these headers could be
  // client-supplied, so they are only trusted when VERCEL=1. They are never
  // passed through; only the signed trio below reaches the backend.
  let edgeClientIp = "";
  if (process.env.VERCEL === "1") {
    const raw =
      req.headers.get("x-vercel-forwarded-for") ??
      req.headers.get("x-real-ip") ??
      req.headers.get("x-forwarded-for") ??
      "";
    edgeClientIp = raw.split(",")[0].trim();
  } else {
    // Cohost: trust the chain only behind our Traefik router, proven by the
    // edge secret that router rewrites. No secret or a wrong one means no
    // client IP, so nothing is signed and the backend keeps its peer address.
    edgeClientIp = edgeClientIP(req.headers, process.env.TIDAL_EDGE_SECRET ?? "");
  }
  if (edgeClientIp) headers.set("x-forwarded-for", edgeClientIp);
  // Signed client IP for backend rate limits behind Traefik: the backend trusts
  // the address only when the HMAC verifies. Without the secret, send none so
  // the backend falls back to the connecting peer.
  const proxySecret = process.env.TIDAL_PROXY_SECRET;
  if (edgeClientIp && proxySecret) {
    const ts = Math.floor(Date.now() / 1000).toString();
    const sig = createHmac("sha256", proxySecret).update(`${edgeClientIp}|${ts}`).digest("hex");
    headers.set("x-tidal-client-ip", edgeClientIp);
    headers.set("x-tidal-client-ts", ts);
    headers.set("x-tidal-client-sig", sig);
  }
  headers.set("x-forwarded-host", incoming.host);
  headers.set("x-forwarded-proto", incoming.protocol.replace(":", ""));

  const init: RequestInit & { duplex?: "half" } = {
    method: req.method,
    headers,
    redirect: "manual",
    cache: "no-store",
    signal: AbortSignal.timeout(UPSTREAM_TIMEOUT_MS),
  };
  if (req.method !== "GET" && req.method !== "HEAD") {
    init.body = req.body;
    init.duplex = "half";
  }

  let upstream: Response;
  try {
    upstream = await fetch(target, init);
  } catch (err) {
    const timedOut = err instanceof Error && (err.name === "TimeoutError" || err.name === "AbortError");
    return json(timedOut ? 504 : 502, timedOut ? "backend_timeout" : "backend_unavailable");
  }

  const out = new Headers();
  upstream.headers.forEach((v, k) => {
    if (!HOP.has(k) && k !== "set-cookie") out.set(k, v);
  });
  // Explicit per-cookie copy so every Set-Cookie survives the proxy.
  for (const c of upstream.headers.getSetCookie()) out.append("set-cookie", c);
  out.set("cache-control", "no-store");
  return new Response(upstream.body, { status: upstream.status, headers: out });
}

export { proxy as GET, proxy as POST, proxy as PUT, proxy as PATCH, proxy as DELETE };
