"use client";
import { useState } from "react";
import Link from "next/link";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

// Reconciliation reports pack: the same contents choices from posted data, one period at a time.
// Trial balance packs the YTD ledger lines into debits and credits; bank reconciliation summarises
// the statement lines in range plus the bank ledger balance. Manual dates and packs are the screen's job.
type Line = { code: string; name: string; type: string; debit_cents: number; credit_cents: number };
export default function Reconciliation() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
 const [report, setReport] = useState("trial-balance");
 const [from, setFrom] = useState(""), [to, setTo] = useState("");
 const [tb, setTb] = useState<{ rows: Line[] } | null>(null);
 const [bank, setBank] = useState<{ matched_in_cents: number; matched_out_cents: number; unmatched_in_cents: number; unmatched_out_cents: number; matched: number; unmatched: number; bank_balance_cents: number } | null>(null);
 const [busy, setBusy] = useState(false), [error, setError] = useState("");
 async function run(e: React.FormEvent) {
  e.preventDefault(); if (!orgId || busy) return; setTb(null); setBank(null); setError(""); setBusy(true);
  try {
   if (report === "trial-balance") setTb(await api(`/v1/reports/ledger-summary?org_id=${orgId}&from=${from}&to=${to}`));
   else setBank(await api(`/v1/reports/bank-reconciliation?org_id=${orgId}&from=${from}&to=${to}`));
  } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); }
 }
 const filters = <form onSubmit={run} className="inline-form">
  <div><label htmlFor="rec-report">Report</label><select id="rec-report" disabled={busy} value={report} onChange={e => { setReport(e.target.value); setTb(null); setBank(null); }}>
   <option value="trial-balance">Trial balance</option><option value="bank-reconciliation">Bank reconciliation</option>
  </select></div>
  <div><label htmlFor="rec-from">From</label><DateInput id="rec-from" required disabled={busy} value={from} onChange={e => { setFrom(e.target.value); setTb(null); setBank(null); }}/></div>
  <div><label htmlFor="rec-to">To</label><DateInput id="rec-to" required disabled={busy} value={to} onChange={e => { setTo(e.target.value); setTb(null); setBank(null); }}/></div>
  <button disabled={busy || !orgId}>{busy ? "Running…" : "Update"}</button></form>;
 return <ReportFrame title="Reconciliation reports" description="Pack-style contents: one report for one period." filters={filters}>
  <p>Bank reconciliation calls the same <Link href="/reports/bank-reconciliation">bank reconciliation report</Link>. No contents editor, compare dates, columns, grouping or export.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {tb && <div className="table-scroll"><table className="doc-table"><caption>Trial balance</caption><thead><tr><th>Code</th><th>Account</th><th>Type</th><th>Debit</th><th>Credit</th></tr></thead><tbody>
   {tb.rows.map((r, i) => <tr key={i}><td>{r.code}</td><th scope="row">{r.name}</th><td>{r.type}</td><td>{r.debit_cents ? formatCents(r.debit_cents) : "—"}</td><td>{r.credit_cents ? formatCents(r.credit_cents) : "—"}</td></tr>)}</tbody>
   <tfoot><tr><th scope="row" colSpan={3}>Totals</th><td>{formatCents(tb.rows.reduce((s, r) => s + r.debit_cents, 0))}</td><td>{formatCents(tb.rows.reduce((s, r) => s + r.credit_cents, 0))}</td></tr></tfoot></table></div>}
  {bank && <div className="table-scroll"><table className="doc-table"><caption>Bank reconciliation</caption><tbody>
   <tr><th scope="row">Matched money in</th><td>{formatCents(bank.matched_in_cents)}</td></tr>
   <tr><th scope="row">Matched money out</th><td>{formatCents(bank.matched_out_cents)}</td></tr>
   <tr><th scope="row">Unmatched money in</th><td>{formatCents(bank.unmatched_in_cents)}</td></tr>
   <tr><th scope="row">Unmatched money out</th><td>{formatCents(bank.unmatched_out_cents)}</td></tr>
   <tr><th scope="row">Bank ledger balance</th><td>{formatCents(bank.bank_balance_cents)}</td></tr>
  </tbody></table></div>}
 </ReportFrame>;
}
