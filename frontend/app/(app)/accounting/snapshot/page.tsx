"use client";
import Link from "next/link";
import { useEffect, useState } from "react";
import { useOrg } from "@/components/OrgProvider";
import PageHeader from "@/components/PageHeader";
import Kpi from "@/components/Kpi";
import Chart from "@/components/charts/Chart";
import { periodLabel } from "@/components/charts/model";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";
import { fiscalYearStart } from "@/lib/fiscal";
import type { PnlData } from "@/lib/report-shape";
import type { AnalyticsData } from "@/components/useAnalytics";

type PnL = PnlData;
export default function Snapshot() {
  const { selected } = useOrg();
  return <Workspace key={selected?.id ?? "none"} orgId={selected?.id} />;
}
function Workspace({ orgId }: { orgId: number | undefined }) {
  const [date, setDate] = useState(() => { const d = new Date(); return `${d.getFullYear()}-${String(d.getMonth()+1).padStart(2,"0")}-${String(d.getDate()).padStart(2,"0")}`; });
  const [data, setData] = useState<PnL | null>(null);
  const [series, setSeries] = useState<AnalyticsData["series"]>([]);
  const [error, setError] = useState("");
  const [seriesError, setSeriesError] = useState("");
  const [fiscalYearEnd, setFiscalYearEnd] = useState("12-31");
  useEffect(() => {
    if (!orgId) return;
    let active = true;
    api<{ fiscal_year_end: string }>(`/v1/organizations/${orgId}`).then(r => { if (active) setFiscalYearEnd(r.fiscal_year_end); }).catch(() => {});
    return () => { active = false; };
  }, [orgId]);
  // The snapshot covers the company's own financial year, not the calendar year.
  const from = fiscalYearStart(date, fiscalYearEnd);
  useEffect(() => {
    if (!orgId || !/^\d{4}-\d{2}-\d{2}$/.test(date)) return;
    let active = true;
    api<PnL>(`/v1/reports/pnl?org_id=${orgId}&from=${from}&to=${date}`).then(value => { if (active) setData(value); }).catch((e: ApiError) => { if (active) setError(messageFor(e.error,e.status)); });
    api<AnalyticsData>(`/v1/reports/analytics?org_id=${orgId}&from=${from}&to=${date}`)
      .then(value => { if (active) setSeries(value.series); })
      .catch((e: ApiError) => { if (active) setSeriesError(messageFor(e.error, e.status)); });
    return () => { active = false; };
  }, [orgId,date,from]);
  const margin = data && data.revenue_cents !== 0 ? `${(100 * data.profit_cents / data.revenue_cents).toFixed(1)}%` : "Not available";
  const period = periodLabel(from, date);
  return <><PageHeader title="Business snapshot" description="Posted revenue and expenses for the financial year to date." />
    {!orgId ? <p className="notice">Choose a company first.</p> : <>
      <label htmlFor="snapshot-date">As at</label> <DateInput id="snapshot-date" value={date} onChange={e => { setData(null); setSeries([]); setError(""); setSeriesError(""); setDate(e.target.value); }}/>
      {error && <p role="alert" className="form-error">{error}</p>}{!data && date && !error && <p role="status">Loading…</p>}
      {data && <>
        <p className="section-help">Financial year to date: {period}. Postings after today are left out.</p>
        <div className="kpi-grid" aria-label="Snapshot figures">
          <Kpi label="Revenue" value={formatCents(data.revenue_cents)} note={period} />
          <Kpi label="Gross profit" value={formatCents(data.gross_profit_cents)} note={period} />
          <Kpi label="Net profit" value={formatCents(data.profit_cents)} note={period} />
          <Kpi label="Net profit margin" value={margin} note={period} />
        </div>
        <h2>Revenue vs spending</h2>
        {seriesError && <p role="alert" className="form-error">{seriesError}</p>}
        <Chart
          series={series.map(m => ({ label: m.month, revenue: m.revenue_cents, expense: m.expense_cents }))}
          title="Revenue vs spending"
          description={`Financial year to date: ${period}.`}
          empty="No posted revenue or spending this financial year yet. Post an invoice, bill or expense to start the chart."
        />
        <h2>Profit and loss</h2>
        <div className="table-scroll"><table className="doc-table"><caption>Profit and loss for {period}</caption><tbody>
          <tr><th scope="row">Revenue</th><td className="num">{formatCents(data.revenue_cents)}</td></tr>
          <tr><th scope="row">Cost of sales</th><td className="num">{formatCents(data.cost_of_sales_cents)}</td></tr>
          <tr><th scope="row">Gross profit</th><td className="num">{formatCents(data.gross_profit_cents)}</td></tr>
          <tr><th scope="row">Operating expenses</th><td className="num">{formatCents(data.operating_expenses_cents)}</td></tr>
          <tr><th scope="row">Operating profit</th><td className="num">{formatCents(data.operating_profit_cents)}</td></tr>
          <tr><th scope="row">Other income</th><td className="num">{formatCents(data.other_income_cents)}</td></tr>
          <tr><th scope="row">Other expense</th><td className="num">{formatCents(data.other_expense_cents)}</td></tr>
          <tr><th scope="row">Net profit</th><td className="num">{formatCents(data.profit_cents)}</td></tr>
          <tr><th scope="row">Net profit margin</th><td className="num">{margin}</td></tr>
        </tbody></table></div>
      </>}
      <p>Accrual basis. The period follows your financial year (start {from}). Gross profit is revenue less cost of sales; net profit also deducts operating expenses and adds other income. Comparisons and editable metrics are not available.</p>
      <p><Link href="/reports/profit-loss">Open profit and loss report</Link></p>
    </>}
  </>;
}
