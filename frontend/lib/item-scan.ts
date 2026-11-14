export function scanItem<T extends { sku: string }>(items: T[], code: string): T | null {
  const needle = code.trim().toLowerCase();
  if (!needle) return null;
  const matches = items.filter(item => item.sku !== "" && item.sku.toLowerCase() === needle);
  return matches.length === 1 ? matches[0] : null;
}
