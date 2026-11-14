"use client";
import { useEffect, useState } from "react";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { formatCents } from "@/lib/money";
import { todayISO } from "@/lib/dates";
import { financialYearToDate } from "@/lib/fiscal";
import type { AnalyticsData } from "@/components/useAnalytics";
import Chart from "@/components/charts/Chart";
import { periodLabel } from "@/components/charts/model";
import Kpi from "@/components/Kpi";

const CATALOG = [
  { key: "bank", label: "Bank balance" },
  { key: "receivable", label: "Owed to you" },
  { key: "payable", label: "Owed by you" },
  { key: "net", label: "Net profit" },
  { key: "gross", label: "Gross profit" },
  { key: "revenue", label: "Revenue" },
  { key: "expense", label: "Spending" },
  { key: "chart", label: "Revenue vs spending chart" },
] as const;
type Key = (typeof CATALOG)[number]["key"];
const DEFAULTS: Key[] = ["bank", "receivable", "payable", "net"];
const STORE = "tidal.home.widgets";

// Home tiles read the same analytics endpoint as the dashboards, so a tile can never disagree with
// the report it links to. The layout is a saved list of tile names, not a free-form grid.
export default function HomeWidgets({ orgId }: { orgId: number }) {
  const [chosen, setChosen] = useState<Key[]>(DEFAULTS);
  const [wide, setWide] = useState<Key[]>([]);
  const [editing, setEditing] = useState(false);
  const [data, setData] = useState<AnalyticsData | null>(null);
  const [error, setError] = useState("");
  const [fiscalYearEnd, setFiscalYearEnd] = useState("12-31");
  const period = financialYearToDate(todayISO(), fiscalYearEnd);

  useEffect(() => {
    try {
      const raw = localStorage.getItem(STORE);
      if (raw) {
        const parsed = JSON.parse(raw) as Key[] | { order?: Key[]; wide?: Key[] };
        const order = Array.isArray(parsed) ? parsed : parsed.order ?? DEFAULTS;
        setChosen(order.filter(k => CATALOG.some(c => c.key === k)));
        if (!Array.isArray(parsed)) setWide((parsed.wide ?? []).filter(k => CATALOG.some(c => c.key === k)));
      }
    } catch { /* first visit or storage disabled: defaults stand */ }
  }, []);
  useEffect(() => {
    let live = true;
    api<{ fiscal_year_end: string }>(`/v1/organizations/${orgId}`)
      .then(r => { if (live) setFiscalYearEnd(r.fiscal_year_end); })
      .catch(() => { /* keep the December fallback: the report still caps at today */ });
    return () => { live = false; };
  }, [orgId]);
  function save(next: Key[], wideNext = wide) {
    setChosen(next); setWide(wideNext);
    try { localStorage.setItem(STORE, JSON.stringify({ order: next, wide: wideNext })); } catch { /* storage disabled: keep in memory */ }
  }
  function move(key: Key, by: number) {
    const i = chosen.indexOf(key), j = i + by;
    if (i < 0 || j < 0 || j >= chosen.length) return;
    const next = [...chosen]; [next[i], next[j]] = [next[j], next[i]]; save(next);
  }
  useEffect(() => {
    let live = true;
    api<AnalyticsData>(`/v1/reports/analytics?org_id=${orgId}&from=${period.from}&to=${period.to}`)
      .then(d => { if (live) setData(d); })
      .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [orgId, period.from, period.to]);

  const value: Record<Exclude<Key, "chart">, number> = {
    bank: data?.totals.bank_cents ?? 0,
    receivable: data?.totals.receivable_cents ?? 0,
    payable: data?.totals.payable_cents ?? 0,
    net: data?.totals.net_cents ?? 0,
    gross: data?.totals.gross_profit_cents ?? 0,
    revenue: data?.totals.revenue_cents ?? 0,
    expense: data?.totals.expense_cents ?? 0,
  };
  const note = periodLabel(period.from, period.to);
  return <section aria-label="Home widgets">
    <div className="record-toolbar"><h2>Your numbers</h2><button className="button-secondary" onClick={() => setEditing(v => !v)}>{editing ? "Done" : "Edit homepage"}</button></div>
    {error && <p role="alert" className="form-error">{error}</p>}
    <p className="section-help">Financial year to date: {note}. Postings after today are left out.</p>
    {editing && <div className="inline-form" role="group" aria-label="Add widget">
      <p className="section-help">Choose the widgets shown on your home page. This layout is saved in this browser.</p>
      {CATALOG.map(c => <label key={c.key} className="check"><input type="checkbox" checked={chosen.includes(c.key)} onChange={e => save(e.target.checked ? [...chosen, c.key] : chosen.filter(k => k !== c.key), e.target.checked ? wide : wide.filter(k => k !== c.key))} />{c.label}</label>)}
      <p className="section-help">Order and width are saved with the layout.</p>
      {chosen.map((key, i) => <div key={key} className="inline-form"><span>{CATALOG.find(c => c.key === key)?.label}</span><button type="button" className="button-secondary" disabled={i === 0} onClick={() => move(key, -1)}>Move up</button><button type="button" className="button-secondary" disabled={i === chosen.length - 1} onClick={() => move(key, 1)}>Move down</button><button type="button" className="button-secondary" onClick={() => save(chosen, wide.includes(key) ? wide.filter(k => k !== key) : [...wide, key])}>{wide.includes(key) ? "Normal width" : "Wide"}</button></div>)}
    </div>}
    {!data && !error && <p role="status">Loading your numbers…</p>}
    {data && !chosen.length && <p className="notice">No widgets chosen. Use Edit homepage to add one.</p>}
    {data && chosen.length > 0 && <div className="kpi-grid">
      {CATALOG.filter(c => chosen.includes(c.key)).map(c => c.key === "chart"
        ? <div key={c.key} className={wide.includes(c.key) ? "kpi-wide" : "kpi-wide"} style={wide.includes(c.key) ? undefined : { gridColumn: "span 1" }}>
            <Chart
              series={data.series.map(m => ({ label: m.month, revenue: m.revenue_cents, expense: m.expense_cents }))}
              title="Revenue vs spending"
              description={`Financial year to date: ${note}.`}
              empty="No posted revenue or spending this financial year yet. Post an invoice, bill or expense to start the chart."
            />
          </div>
        : <div key={c.key} style={wide.includes(c.key) ? { gridColumn: "1 / -1" } : undefined}><Kpi label={c.label} value={formatCents(value[c.key as Exclude<Key, "chart">])} note={note} /></div>)}
    </div>}
  </section>;
}
