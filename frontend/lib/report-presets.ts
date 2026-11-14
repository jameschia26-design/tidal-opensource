export type ReportDatePreset = "this_month" | "last_month" | "this_quarter" | "last_quarter" | "month_to_date" | "quarter_to_date" | "ytd" | "fytd" | "this_fy" | "last_fy" | "last_year";
export type DateRange = { from: string; to: string };

const iso = (date: Date) => date.toISOString().slice(0, 10);
const day = (date: string) => new Date(`${date}T00:00:00.000Z`);

function fiscalEnd(year: number, fiscalYearEnd: string): Date {
  const [rawMonth, rawDay] = fiscalYearEnd.split("-").map(Number);
  const month = Number.isInteger(rawMonth) && rawMonth >= 1 && rawMonth <= 12 ? rawMonth : 12;
  const requestedDay = Number.isInteger(rawDay) && rawDay >= 1 && rawDay <= 31 ? rawDay : 31;
  const lastDay = new Date(Date.UTC(year, month, 0)).getUTCDate();
  return new Date(Date.UTC(year, month - 1, Math.min(requestedDay, lastDay)));
}

function addDays(date: Date, amount: number): Date {
  return new Date(date.getTime() + amount * 86_400_000);
}

export function reportDateRange(preset: ReportDatePreset, today: string, fiscalYearEnd = "12-31"): DateRange {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(today) || !Number.isFinite(day(today).getTime()) || iso(day(today)) !== today) throw new RangeError("today must be a valid YYYY-MM-DD date");
  const current = day(today);
  const year = current.getUTCFullYear();
  const month = current.getUTCMonth();
  const to = iso(current);
  switch (preset) {
    case "this_month":
    case "month_to_date": return { from: iso(new Date(Date.UTC(year, month, 1))), to };
    case "last_month": return { from: iso(new Date(Date.UTC(year, month - 1, 1))), to: iso(new Date(Date.UTC(year, month, 0))) };
    case "this_quarter":
    case "quarter_to_date": return { from: iso(new Date(Date.UTC(year, Math.floor(month / 3) * 3, 1))), to };
    case "last_quarter": {
      const quarterStart = Math.floor(month / 3) * 3;
      return { from: iso(new Date(Date.UTC(year, quarterStart - 3, 1))), to: iso(new Date(Date.UTC(year, quarterStart, 0))) };
    }
    case "ytd": return { from: iso(new Date(Date.UTC(year, 0, 1))), to };
    case "fytd":
    case "this_fy": {
      const end = fiscalEnd(year, fiscalYearEnd);
      const start = end.getTime() >= current.getTime() ? fiscalEnd(year - 1, fiscalYearEnd) : end;
      return { from: iso(addDays(start, 1)), to };
    }
    case "last_fy": {
      const end = fiscalEnd(year, fiscalYearEnd);
      const currentStart = addDays(end.getTime() >= current.getTime() ? fiscalEnd(year - 1, fiscalYearEnd) : end, 1);
      const previousEnd = addDays(currentStart, -1);
      const previousStart = addDays(fiscalEnd(previousEnd.getUTCFullYear() - 1, fiscalYearEnd), 1);
      return { from: iso(previousStart), to: iso(previousEnd) };
    }
    case "last_year": return { from: `${year - 1}-01-01`, to: `${year - 1}-12-31` };
    default: throw new RangeError("unsupported report date preset");
  }
}
