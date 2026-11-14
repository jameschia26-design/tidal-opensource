import { backendBase, UpstreamConfigError } from "./upstream.ts";

// Only these two paths are reachable through the health host. Traefik also
// restricts them at the edge; this is the second, independent check.
const HEALTH_PATHS = new Set(["/readyz", "/healthz"]);
const TIMEOUT_MS = 5_000;

export async function GET(req: Request): Promise<Response> {
  const path = new URL(req.url).pathname;
  if (!HEALTH_PATHS.has(path)) {
    return Response.json({ error: "not_found" }, { status: 404 });
  }

  let base: URL;
  try {
    base = backendBase();
  } catch (err) {
    if (err instanceof UpstreamConfigError) {
      return Response.json({ error: "backend_not_configured" }, { status: 503 });
    }
    return Response.json({ error: "internal_error" }, { status: 500 });
  }

  try {
    const upstream = await fetch(new URL(path, base), {
      cache: "no-store",
      signal: AbortSignal.timeout(TIMEOUT_MS),
    });
    return new Response(upstream.body, {
      status: upstream.status,
      headers: {
        "content-type": upstream.headers.get("content-type") ?? "application/json",
        "cache-control": "no-store",
      },
    });
  } catch {
    return Response.json({ error: "backend_unavailable" }, { status: 502 });
  }
}
