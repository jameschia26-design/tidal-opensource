"use client";
import ReportFrame from "@/components/ReportFrame";
import Kpi from "@/components/Kpi";
import Chart from "@/components/charts/Chart";
import { periodLabel } from "@/components/charts/model";
import { useOrgId } from "@/components/useOrgId";
import { DateRange, useAnalytics } from "@/components/useAnalytics";
import { formatCents } from "@/lib/money";

export default function Dashboards() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
  const state = useAnalytics(orgId);
  const { data, busy, error } = state;
  const period = data ? periodLabel(data.from, data.to) : "";
  return <ReportFrame title="Dashboards" description="One screen of headline tiles for the chosen period, built from the same posted books as the reports." filters={<DateRange id="db" state={state} />}>
    {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}{busy && <p>Running…</p>}
    {data && <>
      <div className="kpi-grid" aria-label="Headline figures">
        <Kpi label="Revenue" value={formatCents(data.totals.revenue_cents)} note={period} />
        <Kpi label="Spending" value={formatCents(data.totals.expense_cents)} note={period} />
        <Kpi label="Net profit" value={formatCents(data.totals.net_cents)} note={period} />
        <Kpi label="Bank" value={formatCents(data.totals.bank_cents)} note={period} />
        <Kpi label="Invoices owed" value={formatCents(data.totals.receivable_cents)} note={period} />
        <Kpi label="Bills to pay" value={formatCents(data.totals.payable_cents)} note={period} />
      </div>
      <h2>Revenue vs spending</h2>
      <Chart series={data.series.map(m => ({ label: m.month, revenue: m.revenue_cents, expense: m.expense_cents }))} title="Revenue vs spending" empty="No posted revenue or spending in this period." />
      <div className="table-scroll"><table className="doc-table"><caption>Month by month</caption><thead><tr><th scope="col">Month</th><th scope="col" className="num">Revenue</th><th scope="col" className="num">Spending</th><th scope="col" className="num">Net</th></tr></thead><tbody>
        {data.series.map(m => <tr key={m.month}><th scope="row">{m.month}</th><td className="num">{formatCents(m.revenue_cents)}</td><td className="num">{formatCents(m.expense_cents)}</td><td className="num">{formatCents(m.revenue_cents - m.expense_cents)}</td></tr>)}
        {!data.series.length && <tr><td colSpan={4}>No posted activity in this period.</td></tr>}
      </tbody></table></div>
    </>}
  </ReportFrame>;
}
