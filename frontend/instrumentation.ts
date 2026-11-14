// Fail-fast cohost startup guard. Runs once per server instance (see
// next/dist/docs/01-app/02-guides/instrumentation.md): refuses a web container
// whose ingress secrets cannot prove edge traffic (length-checked here, the
// constant-time compare happens per request in lib/client-ip.ts).
export async function register() {
  if (process.env.NEXT_RUNTIME !== "nodejs") return;
  if (process.env.TIDAL_COHOST !== "1") return;
  const proxy = process.env.TIDAL_PROXY_SECRET ?? "";
  const edge = process.env.TIDAL_EDGE_SECRET ?? "";
  if (proxy.length < 32 || edge.length < 32 || proxy === edge) {
    throw new Error(
      "TIDAL_PROXY_SECRET and TIDAL_EDGE_SECRET must each be 32+ characters and differ",
    );
  }
  const backend = process.env.BACKEND_URL ?? "";
  if (backend !== "http://api:8080") {
    throw new Error("BACKEND_URL must be the private cohost origin http://api:8080");
  }
}
