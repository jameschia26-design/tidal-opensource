import { timingSafeEqual } from "node:crypto";
import { isIP } from "node:net";

// Cohost ingress trust. Traefik is the only hop allowed to speak for the client,
// but it shares the docker overlay with other apps, so a shared subnet is NOT a
// trust boundary. Instead our router rewrites x-tidal-edge-secret (Traefik's
// customRequestHeaders overwrite any client copy); only a request carrying the
// exact secret is treated as edge traffic. Everything else fails closed.
export const EDGE_SECRET_HEADER = "x-tidal-edge-secret";

function equalsSecret(a: string, b: string): boolean {
  const x = Buffer.from(a);
  const y = Buffer.from(b);
  return x.length === y.length && timingSafeEqual(x, y);
}

// Traefik appends the peer address, so the RIGHTMOST entry is the address it
// accepted; entries to the left were supplied by the client and are never used.
// A malformed rightmost hop yields "" (no signature) rather than a guess.
export function edgeClientIP(headers: Headers, edgeSecret: string): string {
  if (!edgeSecret) return "";
  if (!equalsSecret(headers.get(EDGE_SECRET_HEADER) ?? "", edgeSecret)) return "";
  const chain = headers.get("x-forwarded-for") ?? "";
  const last = chain.split(",").pop()?.trim().replace(/^\[|\]$/g, "") ?? "";
  return isIP(last) ? last : "";
}
