// Pure helpers behind the hand-built charts. No JSX and no React, so node --test can import this
// file directly. Charts read the same analytics series as the tables, so axes and rows always agree.
export type ChartPoint = { label: string; revenue: number; expense: number; cost?: number; gross?: number };

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

// Round the tallest value up to a readable axis maximum: 1, 2, 2.5 or 5 times a power of ten.
export function niceMax(value: number, ticks = 4): number {
  if (!Number.isFinite(value) || value <= 0) return 1;
  const rough = value / ticks;
  const power = 10 ** Math.floor(Math.log10(rough));
  for (const step of [1, 2, 2.5, 5, 10]) {
    if (rough <= step * power) return step * power * ticks;
  }
  return 10 * power * ticks;
}

export function axisTicks(max: number, count = 4): number[] {
  const safe = max > 0 ? max : 1;
  return Array.from({ length: count + 1 }, (_, i) => Math.round((safe * i * 100) / count) / 100);
}

// Axis and KPI money is compact: S$0, S$850, S$1.2k, S$1.25m. Full cents stay in tables.
export function compactMoney(cents: number): string {
  const dollars = Math.abs(cents) / 100;
  const sign = cents < 0 ? "-" : "";
  if (dollars >= 1_000_000) return `${sign}S$${trim(dollars / 1_000_000)}m`;
  if (dollars >= 1_000) return `${sign}S$${trim(dollars / 1_000)}k`;
  return `${sign}S$${Math.round(dollars)}`;
}

function trim(n: number): string {
  if (n >= 100) return n.toFixed(0);
  if (n >= 10) return String(Math.round(n * 10) / 10);
  return String(Math.round(n * 100) / 100);
}

// Analytics months arrive as YYYY-MM; people read Jan, Feb, ...
export function monthLabel(month: string): string {
  const match = /^(\d{4})-(\d{2})$/.exec(month);
  if (!match) return month;
  return MONTHS[Number(match[2]) - 1] ?? month;
}

export function dayLabel(iso: string): string {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso);
  if (!match) return iso;
  return `${Number(match[3])} ${MONTHS[Number(match[2]) - 1] ?? match[2]}`;
}

// "1 Jan – 3 Nov 2026". One year at the end when both ends share it, otherwise both years.
export function periodLabel(from: string, to: string): string {
  const fromYear = from.slice(0, 4);
  const toYear = to.slice(0, 4);
  if (!/^\d{4}-\d{2}-\d{2}$/.test(from) || !/^\d{4}-\d{2}-\d{2}$/.test(to)) return `${from} to ${to}`;
  return fromYear === toYear
    ? `${dayLabel(from)} – ${dayLabel(to)} ${toYear}`
    : `${dayLabel(from)} ${fromYear} – ${dayLabel(to)} ${toYear}`;
}
