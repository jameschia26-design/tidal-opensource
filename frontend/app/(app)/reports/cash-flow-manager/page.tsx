"use client";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { DateRange, useAnalytics } from "@/components/useAnalytics";
import { formatCents } from "@/lib/money";
import { formatDate } from "@/lib/dates";

export default function CashFlowManager() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
  const state = useAnalytics(orgId);
  const { data, busy, error } = state;
  // Closing balance after each forecast month, computed up front so render stays pure.
  const running = data ? data.forecast.map((_, i) => data.totals.bank_cents + data.forecast.slice(0, i + 1).reduce((sum, f) => sum + f.in_cents - f.out_cents, 0)) : [];
  return <ReportFrame title="Cash flow manager" description="What is due in and out over the next six months, on top of the bank balance." filters={<DateRange id="cf" state={state} />}>
    <p>Due-document forecast by due month.</p>
    {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}{busy && <p>Running…</p>}
    {data && <>
      <p><strong>Bank balance at {formatDate(data.to)}:</strong> {formatCents(data.totals.bank_cents)}</p>
      <div className="table-scroll"><table className="doc-table"><caption>Forecast by month</caption><thead><tr><th scope="col">Month</th><th scope="col">Expected in</th><th scope="col">Expected out</th><th scope="col">Closing balance</th></tr></thead><tbody>
        {data.forecast.map((f, i) => <tr key={f.month}><th scope="row">{f.month}</th><td>{formatCents(f.in_cents)}</td><td>{formatCents(f.out_cents)}</td><td>{formatCents(running[i])}</td></tr>)}
        {!data.forecast.length && <tr><td colSpan={4}>No open documents fall due in the next six months.</td></tr>}
      </tbody></table></div>
      <h2>Open documents at {formatDate(data.to)}</h2>
      <div className="table-scroll"><table className="doc-table"><caption>Open documents</caption><tbody>
        <tr><th scope="row">Invoices owed to you</th><td>{formatCents(data.totals.receivable_cents)}</td></tr>
        <tr><th scope="row">Bills you owe</th><td>{formatCents(data.totals.payable_cents)}</td></tr>
      </tbody></table></div>
    </>}
  </ReportFrame>;
}
