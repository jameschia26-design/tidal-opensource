// Dates are stored and sent as ISO yyyy-mm-dd; people read and type dd/mm/yyyy.
// "Today" means today in Singapore, whatever time zone the browser or server is in.
const SG_OFFSET_MS = 8 * 60 * 60 * 1000;

export function todayISO(): string {
  return new Date(Date.now() + SG_OFFSET_MS).toISOString().slice(0, 10);
}

// A date the app stores is exactly yyyy-mm-dd. A timestamp is not a date: it must be rejected, not cut down.
export function isDateOnly(value: string | null | undefined): boolean {
  return typeof value === "string" && /^\d{4}-\d{2}-\d{2}$/.test(value);
}

// A min/max bound is compared as text, so it must itself be date-only. A timestamp bound would
// compare wrongly against a typed date, so it is rejected instead of silently truncating it.
export function dateOnlyBound(value: string | null | undefined): string | undefined {
  return typeof value === "string" && isDateOnly(value) ? value : undefined;
}

export function formatDate(iso: string | null | undefined): string {
  if (!iso) return "";
  // Reject datetimes rather than silently dropping the time: callers and tests must use date-only values.
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso);
  if (!m) return iso;
  return `${m[3]}/${m[2]}/${m[1]}`;
}

// Strict dd/mm/yyyy. Rejects 31/02/2025 and non-leap 29/02/2026 by round-tripping the date.
export function parseDMY(text: string): string | null {
  const m = /^(\d{1,2})\/(\d{1,2})\/(\d{4})$/.exec(text.trim());
  if (!m) return null;
  const d = Number(m[1]), mo = Number(m[2]), y = Number(m[3]);
  if (y < 1 || mo < 1 || mo > 12 || d < 1) return null;
  const dt = new Date(Date.UTC(y, mo - 1, d));
  if (dt.getUTCFullYear() !== y || dt.getUTCMonth() !== mo - 1 || dt.getUTCDate() !== d) return null;
  return `${m[3]}-${String(mo).padStart(2, "0")}-${String(d).padStart(2, "0")}`;
}

export function addDaysISO(iso: string, days: number): string {
  const d = new Date(`${iso}T00:00:00Z`);
  if (Number.isNaN(d.getTime())) return "";
  d.setUTCDate(d.getUTCDate() + days);
  return d.toISOString().slice(0, 10);
}

// Auto-insert slashes as user types dd/mm/yyyy.
export function autoSlashDate(raw: string, prev: string = ""): string {
  if (raw.length < prev.length) return raw;
  const trimmed = raw.trim();
  if (/^\d{4}-\d{2}-\d{2}$/.test(trimmed)) {
    const [y, m, d] = trimmed.split("-");
    return `${d}/${m}/${y}`;
  }
  const digits = raw.replace(/\D/g, "").slice(0, 8);
  if (digits.length === 2 && !raw.includes("/")) {
    return `${digits}/`;
  }
  if (digits.length === 4 && (raw.match(/\//g) || []).length < 2) {
    return `${digits.slice(0, 2)}/${digits.slice(2)}/`;
  }
  if (digits.length > 4) {
    const d = digits.slice(0, 2);
    const m = digits.slice(2, 4);
    const y = digits.slice(4);
    return `${d}/${m}/${y}`;
  }
  return raw;
}
