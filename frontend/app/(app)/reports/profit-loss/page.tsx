"use client";
import { useState } from "react";
import PageHeader from "@/components/PageHeader";
import PreviewNotice from "@/components/PreviewNotice";
import ReportBlocks from "@/components/ReportBlocks";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { formatCents } from "@/lib/money";
import { pnlBlocks, type PnlData } from "@/lib/report-shape";
import DateInput from "@/components/DateInput";
import { periodLabel } from "@/components/charts/model";
import { TrackingCategoryPicker } from "@/components/TrackingSelect";

type TrackingRow = { code: string; name: string; amounts: Record<string, number>; untracked_cents: number };
type TrackingData = { category_id: number; category: string; options: { id: number; name: string }[]; rows: TrackingRow[] };
type PnL = PnlData & { compare?: PnlData; tracking?: TrackingData };
const DATE = /^\d{4}-\d{2}-\d{2}$/;

export default function ProfitLoss() {
  const orgId = useOrgId();
  return <ProfitLossWorkspace key={orgId ?? "none"} orgId={orgId} />;
}

function ProfitLossWorkspace({ orgId }: { orgId: number | null }) {
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [data, setData] = useState<PnL | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [compare, setCompare] = useState(false);
  const [budget, setBudget] = useState<{ rows: { account_id: number; code: string; name: string; actual_cents: number; budget_cents: number; variance_cents: number }[] } | null>(null);
  const [tracking, setTracking] = useState("");

  async function run(e: React.FormEvent) {
    e.preventDefault();
    if (!DATE.test(from) || !DATE.test(to)) { setError("Choose a start and end date."); return; }
    setBusy(true);
    setError(null);
    try {
      setData(await api<PnL>(`/v1/reports/pnl?from=${from}&to=${to}${compare ? "&compare=previous" : ""}${tracking ? `&tracking=${tracking}` : ""}${orgId != null ? `&org_id=${orgId}` : ""}`));
    } catch (err) {
      setData(null);
      setError(messageFor((err as ApiError).error, (err as ApiError).status));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader title="Profit and loss" description="Revenue, cost of sales, operating expenses and other income and expense from posted entries in the period." period={data ? periodLabel(from, to) : undefined} />
      <PreviewNotice>Draft and unposted documents are not included.</PreviewNotice>
      <form onSubmit={run} className="inline-form" noValidate>
        <div><label htmlFor="pl-from">From</label><DateInput id="pl-from" value={from} disabled={busy} onChange={(e) => { setFrom(e.target.value); setData(null); }}/></div>
        <div><label htmlFor="pl-to">To</label><DateInput id="pl-to" value={to} disabled={busy} onChange={(e) => { setTo(e.target.value); setData(null); }}/></div>
        <TrackingCategoryPicker orgId={orgId} id="pl-tracking" value={tracking} disabled={busy} onChange={(v) => { setTracking(v); setData(null); }} />
        <label className="check"><input type="checkbox" checked={!!budget} disabled={busy} onChange={async (e) => { if (!e.target.checked) { setBudget(null); return; } try { setBudget(await api(`/v1/reports/budget-variance?org_id=${orgId}&from=${from}&to=${to}`)); } catch { setBudget(null); } }} /> Show budget variance</label>
        <label className="check"><input type="checkbox" checked={compare} disabled={busy} onChange={(e) => { setCompare(e.target.checked); setData(null); }} /> Compare with previous year</label>
        <button type="submit" disabled={busy || orgId == null}>{busy ? "Running…" : "Run report"}</button>
      </form>
      {orgId == null && <p className="muted">Choose a company first.</p>}
      {error && <p role="alert" className="form-error">{error}</p>}
      {data && (
        <>
          <ReportBlocks blocks={pnlBlocks(data, data.compare)} showPrevious={!!data.compare} />
          {data.tracking && <section aria-label="Tracking columns">
            <h2>{data.tracking.category}</h2>
            <p className="section-help">Amounts come from the same journal lines as the report above, split by the option on each line. The columns add back to each account&apos;s amount, so they reconcile to the profit and loss.</p>
            <div className="table-scroll"><table className="doc-table"><caption>{data.tracking.category} by account</caption>
              <thead><tr><th scope="col">Account</th>{data.tracking.options.map(o => <th key={o.id} scope="col" className="num">{o.name}</th>)}<th scope="col" className="num">Untracked</th></tr></thead>
              <tbody>{data.tracking.rows.map(r => <tr key={r.code}><td>{r.code} — {r.name}</td>{data.tracking!.options.map(o => <td key={o.id} className="num">{formatCents(r.amounts[o.name] ?? 0)}</td>)}<td className="num">{formatCents(r.untracked_cents)}</td></tr>)}</tbody>
            </table></div>
          </section>}
          {budget && <section aria-label="Budget variance"><h2>Budget variance</h2><div className="table-scroll"><table className="doc-table"><caption>Actual against budget</caption><thead><tr><th>Code</th><th>Account</th><th className="num">Actual</th><th className="num">Budget</th><th className="num">Variance</th></tr></thead><tbody>{budget.rows.map(r => <tr key={r.account_id}><td>{r.code}</td><th scope="row">{r.name}</th><td className="num">{formatCents(r.actual_cents)}</td><td className="num">{formatCents(r.budget_cents)}</td><td className="num">{formatCents(r.variance_cents)}</td></tr>)}</tbody></table></div>{budget.rows.length === 0 && <p className="notice">No budget set for this period.</p>}</section>}
          <p className="section-help">Gross profit is revenue less cost of sales. Operating profit also deducts operating expenses. Net profit adds other income and deducts other expense.</p>
        </>
      )}
    </>
  );
}
