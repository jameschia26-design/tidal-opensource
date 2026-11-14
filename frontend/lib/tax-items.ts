import { parseCents } from "./money";

export function parseTaxItems(items: { label: string; amount: string; reason: string; ref: string }[]) {
  return items.map(item => {
    const amount = parseCents(item.amount);
    if (!item.label.trim() || item.label.length > 200 || amount === null || item.reason.length > 1000 || item.ref.length > 200) throw new Error("Each item needs a description or relief type and a valid non-negative amount.");
    return { label: item.label.trim(), amount, reason: item.reason, ref: item.ref };
  });
}
