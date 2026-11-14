// Fiscal-year helpers. Companies choose a year end (MM-DD); reports must respect it, not assume December.
export function fiscalYearStart(date: string, fiscalYearEnd: string): string {
  const [m, d] = fiscalYearEnd.split("-").map(Number);
  if (!m || !d) return `${date.slice(0, 4)}-01-01`;
  const year = Number(date.slice(0, 4));
  const endThisYear = `${year}-${String(m).padStart(2, "0")}-${String(d).padStart(2, "0")}`;
  // The fiscal year that contains `date` ends on the first year-end on or after it.
  const endYear = date <= endThisYear ? year : year + 1;
  const start = new Date(Date.UTC(endYear - 1, m - 1, d) + 24 * 60 * 60 * 1000);
  return start.toISOString().slice(0, 10);
}

// "To date" on a dashboard means this company's financial year up to today, never a calendar year
// and never a period that runs into the future.
export function financialYearToDate(today: string, fiscalYearEnd: string): { from: string; to: string } {
  return { from: fiscalYearStart(today, fiscalYearEnd), to: today };
}
