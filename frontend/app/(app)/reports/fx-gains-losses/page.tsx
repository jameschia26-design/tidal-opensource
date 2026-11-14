"use client";
// Foreign Currency Gains and Losses: movements on the realised and unrealised FX accounts by currency
// and period, reconciled to those accounts. The month-end revaluation run lives here too.
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import DateInput from "@/components/DateInput";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";

type Row = { currency: string; kind: "realised" | "unrealised"; amount_cents: number };
type Data = {
  rows: Row[]; realised_cents: number; unrealised_cents: number; total_cents: number;
  opening_balance_cents: number; closing_balance_cents: number; movement_cents: number; reconciles: boolean;
};

export default function FXGainsLosses() {
  const orgId = useOrgId();
  return <Workspace key={orgId ?? "none"} orgId={orgId} />;
}

function Workspace({ orgId }: { orgId: number | null }) {
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [data, setData] = useState<Data | null>(null);
  const [periodEnd, setPeriodEnd] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  async function run(e: React.FormEvent) {
    e.preventDefault();
    if (!orgId || busy) return;
    setError(""); setBusy(true);
    try { setData(await api<Data>(`/v1/reports/fx-gains-losses?org_id=${orgId}&from=${from}&to=${to}`)); }
    catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
    finally { setBusy(false); }
  }

  async function revalue(e: React.FormEvent) {
    e.preventDefault();
    if (!orgId || busy || !periodEnd) return;
    setError(""); setNotice(""); setBusy(true);
    try {
      const result = await api<{ total_cents: number; currencies: unknown[] }>("/v1/fx/revalue", {
        method: "POST", body: JSON.stringify({ org_id: orgId, period_end: periodEnd }),
      });
      setNotice(`Revaluation posted ${formatCents(result.total_cents)} across ${result.currencies.length} currenc${result.currencies.length === 1 ? "y" : "ies"}. Rerunning the same period replaces the earlier figure.`);
      if (from && to) setData(await api<Data>(`/v1/reports/fx-gains-losses?org_id=${orgId}&from=${from}&to=${to}`));
    } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
    finally { setBusy(false); }
  }

  const filters = <form onSubmit={run} className="inline-form">
    <div><label htmlFor="fx-from">From</label><DateInput id="fx-from" required disabled={busy} value={from} onChange={e => { setFrom(e.target.value); setData(null); }} /></div>
    <div><label htmlFor="fx-to">To</label><DateInput id="fx-to" required disabled={busy} value={to} onChange={e => { setTo(e.target.value); setData(null); }} /></div>
    <button disabled={busy || !orgId}>{busy ? "Running…" : "Update"}</button>
  </form>;

  return <ReportFrame title="Foreign Currency Gains and Losses" description="Realised and unrealised movements by currency, reconciled to the FX accounts, up to 366 days." filters={filters}>
    {!orgId && <p>Choose a company first.</p>}
    {error && <p role="alert">{error}</p>}
    {notice && <p role="status">{notice}</p>}
    {data && <>
      {data.rows.length === 0 ? <p>No FX movements in this period.</p> : <div className="table-scroll"><table className="doc-table"><caption>FX movements by currency</caption>
        <thead><tr><th scope="col">Currency</th><th scope="col">Kind</th><th scope="col" className="num">Gain / (loss)</th></tr></thead>
        <tbody>{data.rows.map((r, i) => <tr key={i}><td>{r.currency}</td><td>{r.kind === "realised" ? "Realised" : "Unrealised"}</td><td className="num">{formatCents(r.amount_cents)}</td></tr>)}
          <tr><th scope="row">Total</th><td /><td className="num">{formatCents(data.total_cents)}</td></tr></tbody></table></div>}
      <dl className="summary-list">
        <div><dt>Realised</dt><dd>{formatCents(data.realised_cents)}</dd></div>
        <div><dt>Unrealised</dt><dd>{formatCents(data.unrealised_cents)}</dd></div>
        <div><dt>Opening FX balance</dt><dd>{formatCents(data.opening_balance_cents)}</dd></div>
        <div><dt>Closing FX balance</dt><dd>{formatCents(data.closing_balance_cents)}</dd></div>
      </dl>
      <p>{data.reconciles ? "The movements reconcile to the FX accounts." : "The movements do not reconcile to the FX accounts; check for manual journals on those accounts."}</p>
    </>}
    <form className="record-form" onSubmit={revalue}>
      <fieldset disabled={busy || !orgId}><legend>Month-end revaluation</legend>
        <p className="section-help">Revalues every open foreign invoice and bill at its currency&apos;s rate on the period end and posts the difference as unrealised gain or loss. Rerunning the same period reverses the earlier journal, so nothing posts twice.</p>
        <div className="form-grid"><div><label htmlFor="fx-period-end">Period end</label><DateInput id="fx-period-end" required value={periodEnd} onChange={e => setPeriodEnd(e.target.value)} /></div></div>
      </fieldset>
      <button type="submit" disabled={busy || !orgId || !periodEnd}>Run revaluation</button>
    </form>
  </ReportFrame>;
}
