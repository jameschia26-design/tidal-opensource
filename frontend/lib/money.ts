// Money is integer cents end to end. Parse user input without floats.
export function parseCents(input: string): number | null {
  const s = input.trim().replace(/,/g, "");
  if (!/^\d+(\.\d{1,2})?$/.test(s)) return null;
  const [whole, frac = ""] = s.split(".");
  const cents = Number(whole) * 100 + Number(frac.padEnd(2, "0"));
  return Number.isSafeInteger(cents) ? cents : null;
}

export function parseSignedCents(input: string): number | null {
  const text = input.trim();
  const negative = text.startsWith("-");
  const cents = parseCents(negative ? text.slice(1) : text);
  return cents === null ? null : negative ? -cents : cents;
}

export function formatCents(cents: number): string {
  return new Intl.NumberFormat("en-SG", { style: "currency", currency: "SGD" }).format(cents / 100);
}

// Auto-format currency typing with commas for thousands (e.g. "1000" -> "1,000", "1000.5" -> "1,000.5").
export function formatMoneyInput(raw: string): string {
  if (!raw) return "";
  const trimmed = raw.trim();
  const negative = trimmed.startsWith("-");
  const withoutSign = negative ? trimmed.slice(1) : trimmed;
  const clean = withoutSign.replace(/,/g, "");
  const parts = clean.split(".");
  const whole = parts[0].replace(/\D/g, "");
  if (!whole && parts.length === 1) return negative ? "-" : "";
  const formattedWhole = whole.replace(/\B(?=(\d{3})+(?!\d))/g, ",");
  if (parts.length > 1) {
    const frac = parts[1].replace(/\D/g, "").slice(0, 2);
    return `${negative ? "-" : ""}${formattedWhole}.${frac}`;
  }
  return `${negative ? "-" : ""}${formattedWhole}`;
}
