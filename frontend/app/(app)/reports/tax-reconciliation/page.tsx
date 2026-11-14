"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import DateInput from "@/components/DateInput";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { formatCents } from "@/lib/money";

type Box = { box: string; label: string; value_cents: number };
type Data = { period_start: string; period_end: string; boxes: Box[]; output_tax_cents: number; input_tax_cents: number; net_payable_cents: number; reconciles_output: boolean; reconciles_input: boolean; warnings: string[] };
export default function TaxReconciliation() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
  const [from, setFrom] = useState(""), [to, setTo] = useState("");
  const [data, setData] = useState<Data | null>(null), [busy, setBusy] = useState(false), [error, setError] = useState("");
  async function run(e: React.FormEvent) { e.preventDefault(); if (!orgId || busy) return; setData(null); setError(""); setBusy(true); try { setData(await api<Data>(`/v1/reports/tax-reconciliation?org_id=${orgId}&from=${from}&to=${to}`)); } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); } }
  const filters = <form onSubmit={run} className="inline-form"><div><label htmlFor="tr-from">From</label><DateInput id="tr-from" required disabled={busy} value={from} onChange={e => { setFrom(e.target.value); setData(null); }} /></div><div><label htmlFor="tr-to">To</label><DateInput id="tr-to" required disabled={busy} value={to} onChange={e => { setTo(e.target.value); setData(null); }} /></div><button disabled={busy || !orgId}>{busy ? "Running…" : "Reconcile"}</button></form>;
  return <ReportFrame title="Tax reconciliation" description="The GST return boxes against the GST control account movement for the same period." filters={filters}>
    <p>Box 6 is output tax, box 7 input tax and box 8 the net payable. The control account is code 2300; its movement must agree with the return. Manual GST journals are listed as differences rather than silently corrected.</p>
    {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
    {data && <>
      <h2>GST return for {data.period_start} to {data.period_end}</h2>
      <div className="table-scroll"><table className="doc-table"><caption>F5 boxes</caption><thead><tr><th>Box</th><th>Description</th><th className="num">Amount</th></tr></thead><tbody>{data.boxes.map(b => <tr key={b.box}><td>{b.box}</td><th scope="row">{b.label}</th><td className="num">{formatCents(b.value_cents)}</td></tr>)}</tbody></table></div>
      <h2>Control account</h2>
      <div className="table-scroll"><table className="doc-table"><caption>GST control account 2300</caption><thead><tr><th>Measure</th><th className="num">Return</th><th className="num">Ledger</th><th>Agrees</th></tr></thead><tbody>
        <tr><th scope="row">Output tax</th><td className="num">{formatCents(data.output_tax_cents)}</td><td className="num">{formatCents(data.output_tax_cents)}</td><td>{data.reconciles_output ? "Yes" : "No"}</td></tr>
        <tr><th scope="row">Input tax</th><td className="num">{formatCents(data.input_tax_cents)}</td><td className="num">{formatCents(data.input_tax_cents)}</td><td>{data.reconciles_input ? "Yes" : "No"}</td></tr>
        <tr><th scope="row">Net payable</th><td className="num">{formatCents(data.net_payable_cents)}</td><td className="num">{formatCents(data.output_tax_cents - data.input_tax_cents)}</td><td>{data.reconciles_output && data.reconciles_input ? "Yes" : "No"}</td></tr>
      </tbody></table></div>
      {data.warnings.length > 0 && <div className="notice"><h3>Differences</h3><ul>{data.warnings.map((w, i) => <li key={i}>{w}</li>)}</ul></div>}
    </>}
  </ReportFrame>;
}
