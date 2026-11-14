"use client";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { DateRange, useAnalytics } from "@/components/useAnalytics";
import Chart from "@/components/charts/Chart";
import { formatCents } from "@/lib/money";

export default function Analytics() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
  const state = useAnalytics(orgId);
  const { data, error } = state;
  const filters = <DateRange id="an" state={state} />;
  return <ReportFrame title="Analytics" description="Revenue and spending by month, headline balances, a due-document forecast and ratios." filters={filters}>
    <nav className="tabs" aria-label="Analytics sections"><ul>{[["revenue","Revenue"],["cash","Cash"],["profit","Profitability"],["kpis","KPIs"]].map(([id,label]) => <li key={id}><a className="button-secondary" href={`#analytics-${id}`}>{label}</a></li>)}</ul></nav>
    <p>Numbers come from posted journals and open documents, matching the cash, profit and loss and ageing reports. Up to 366 days; there is no custom chart builder.</p>
    {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
    {data && <>
      <h2 id="analytics-revenue">Revenue vs spending</h2>
      <Chart series={data.series.map(m => ({ label: m.month, revenue: m.revenue_cents, expense: m.expense_cents }))} title="Revenue vs spending" empty="No posted revenue or spending in this period." />
      <div className="table-scroll"><table className="doc-table"><caption>Monthly revenue and spending</caption><thead><tr><th scope="col">Month</th><th scope="col" className="num">Revenue</th><th scope="col" className="num">Spending</th><th scope="col" className="num">Net</th></tr></thead><tbody>
        {data.series.map(m => <tr key={m.month}><th scope="row">{m.month}</th><td className="num">{formatCents(m.revenue_cents)}</td><td className="num">{formatCents(m.expense_cents)}</td><td className="num">{formatCents(m.revenue_cents - m.expense_cents)}</td></tr>)}
      </tbody></table></div>
      <h2 id="analytics-profit">Headline totals</h2>
      <div className="table-scroll"><table className="doc-table"><caption>Headline totals</caption><tbody>
        <tr><th scope="row">Revenue</th><td className="num">{formatCents(data.totals.revenue_cents)}</td></tr>
        <tr><th scope="row">Spending</th><td className="num">{formatCents(data.totals.expense_cents)}</td></tr>
        <tr><th scope="row">Net</th><td className="num">{formatCents(data.totals.net_cents)}</td></tr>
        <tr><th scope="row">Bank balance</th><td className="num">{formatCents(data.totals.bank_cents)}</td></tr>
        <tr><th scope="row">Owed by customers</th><td className="num">{formatCents(data.totals.receivable_cents)}</td></tr>
        <tr><th scope="row">Owed to suppliers</th><td className="num">{formatCents(data.totals.payable_cents)}</td></tr>
        <tr><th scope="row">Assets</th><td className="num">{formatCents(data.totals.asset_cents)}</td></tr>
        <tr><th scope="row">Liabilities</th><td className="num">{formatCents(data.totals.liability_cents)}</td></tr>
      </tbody></table></div>
      <h2 id="analytics-cash">Cash forecast</h2>
      <div className="table-scroll"><table className="doc-table"><caption>Cash forecast from due documents</caption><thead><tr><th scope="col">Month</th><th scope="col" className="num">Expected in</th><th scope="col" className="num">Expected out</th></tr></thead><tbody>
        {data.forecast.map(f => <tr key={f.month}><th scope="row">{f.month}</th><td className="num">{formatCents(f.in_cents)}</td><td className="num">{formatCents(f.out_cents)}</td></tr>)}
        {!data.forecast.length && <tr><td colSpan={3}>No documents due in the next six months.</td></tr>}
      </tbody></table></div>
      <h2 id="analytics-kpis">Ratios</h2>
      <div className="table-scroll"><table className="doc-table"><caption>Ratios</caption><tbody>
        <tr><th scope="row">Net margin</th><td className="num">{(data.ratios.net_margin_bp / 100).toFixed(2)}%</td></tr>
        <tr><th scope="row">Current ratio</th><td className="num">{(data.ratios.current_ratio_bp / 100).toFixed(2)}</td></tr>
        <tr><th scope="row">Debtor days</th><td className="num">{data.ratios.debtor_days}</td></tr>
        <tr><th scope="row">Creditor days</th><td className="num">{data.ratios.creditor_days}</td></tr>
        <tr><th scope="row">Cash runway (months)</th><td className="num">{data.ratios.cash_runway_months}</td></tr>
      </tbody></table></div>
    </>}
  </ReportFrame>;
}
