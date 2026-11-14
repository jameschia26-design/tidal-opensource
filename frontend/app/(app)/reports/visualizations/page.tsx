"use client";
import ReportFrame from "@/components/ReportFrame";
import Chart from "@/components/charts/Chart";
import { useOrgId } from "@/components/useOrgId";
import { DateRange, useAnalytics } from "@/components/useAnalytics";
import { formatCents } from "@/lib/money";

export default function Visualizations() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
  const state = useAnalytics(orgId);
  const { data, busy, error } = state;
  return <ReportFrame title="Visualizations" description="Revenue and spending plotted month by month." filters={<DateRange id="vs" state={state} />}>
    <p>Two fixed lines; there is no chart builder, colour picker or saved view.</p>
    {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}{busy && <p>Running…</p>}
    {data && <>
      <Chart kind="line" series={data.series.map(m => ({ label: m.month, revenue: m.revenue_cents, expense: m.expense_cents }))} title="Revenue vs spending" empty="No posted revenue or spending in this period." />
      <div className="table-scroll"><table className="doc-table"><caption>Figures behind the chart</caption><thead><tr><th scope="col">Month</th><th scope="col" className="num">Revenue</th><th scope="col" className="num">Spending</th></tr></thead><tbody>
        {data.series.map(m => <tr key={m.month}><th scope="row">{m.month}</th><td className="num">{formatCents(m.revenue_cents)}</td><td className="num">{formatCents(m.expense_cents)}</td></tr>)}
      </tbody></table></div>
    </>}
  </ReportFrame>;
}
