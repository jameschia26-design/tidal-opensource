// Idempotency key for a payment attempt. Survives reload via sessionStorage (per tab).
// Same body -> same key. Changed body -> new key. Cleared only after a confirmed save.
// Storage can be blocked; every access is wrapped and falls back to in-memory use.

type Stored = { body: string; key: string };
const PREFIX = "tidal.payment-key:";

function newKey(): string {
  return globalThis.crypto?.randomUUID?.() ?? `k-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

function read(slot: string): Stored | null {
  try {
    const raw = window.sessionStorage.getItem(PREFIX + slot);
    if (!raw) return null;
    const v = JSON.parse(raw) as Stored;
    return typeof v.key === "string" && typeof v.body === "string" ? v : null;
  } catch {
    return null;
  }
}

function write(slot: string, v: Stored) {
  try {
    window.sessionStorage.setItem(PREFIX + slot, JSON.stringify(v));
  } catch {
    // Blocked storage: key lives only in the component for this page view.
  }
}

// Returns the key to send for this exact body, creating one if the body changed.
export function keyFor(slot: string, body: string): string {
  const cur = read(slot);
  if (cur && cur.body === body) return cur.key;
  const next = { body, key: newKey() };
  write(slot, next);
  return next.key;
}

// Call only after the server confirmed the payment (201 or 200 with the original result).
export function clearKey(slot: string) {
  try {
    window.sessionStorage.removeItem(PREFIX + slot);
  } catch {
    // Nothing to clear if storage is blocked.
  }
}
