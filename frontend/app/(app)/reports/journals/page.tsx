"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";
import { formatDate } from "@/lib/dates";

type Row = { journal_id: number; entry_date: string; posted_date: string; posted_by: string; memo: string; source: string; line_no: number; account: string; code: string; debit_cents: number; credit_cents: number };
export default function Journals() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
 const [from, setFrom] = useState(""), [to, setTo] = useState(""), [order, setOrder] = useState("date_asc");
 const [data, setData] = useState<{ rows: Row[]; debit_cents: number; credit_cents: number; truncated: boolean } | null>(null);
 const [busy, setBusy] = useState(false), [error, setError] = useState("");
 async function run(e: React.FormEvent) {
  e.preventDefault(); if (!orgId || busy) return; setData(null); setError(""); setBusy(true);
  try { setData(await api(`/v1/reports/journals?org_id=${orgId}&from=${from}&to=${to}&order=${order}`)); }
  catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); }
 }
 const filters = <form onSubmit={run} className="inline-form">
  <div><label htmlFor="jr-from">From</label><DateInput id="jr-from" required disabled={busy} value={from} onChange={e => { setFrom(e.target.value); setData(null); }}/></div>
  <div><label htmlFor="jr-to">To</label><DateInput id="jr-to" required disabled={busy} value={to} onChange={e => { setTo(e.target.value); setData(null); }}/></div>
  <div><label htmlFor="jr-order">Order</label><select id="jr-order" disabled={busy} value={order} onChange={e => { setOrder(e.target.value); setData(null); }}><option value="date_asc">Date, oldest first</option><option value="date_desc">Date, newest first</option><option value="id_asc">Journal id, oldest first</option><option value="id_desc">Journal id, newest first</option></select></div>
  <button disabled={busy || !orgId}>{busy ? "Running…" : "Update"}</button></form>;
 return <ReportFrame title="Journal report" description="Every posted journal line with its source, up to 366 days." filters={filters}>
  <p>Includes journals posted by invoices, bills, payments and manual journals. Posted date and posted by are the moment and the user that wrote the journal; the date column is the accounting date. No reversal marker, column chooser or export.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {data && <div className="table-scroll"><table className="doc-table"><caption>Journal report</caption><thead><tr><th>Journal</th><th>Date</th><th>Posted date</th><th>Posted by</th><th>Source</th><th>Memo</th><th>Account</th><th>Debit</th><th>Credit</th></tr></thead><tbody>
   {data.rows.map((r, i) => <tr key={i}><td>{r.journal_id}</td><td>{formatDate(r.entry_date)}</td><td>{formatDate(r.posted_date)}</td><td>{r.posted_by || "—"}</td><td>{r.source}</td><td>{r.memo}</td><th scope="row">{r.code} {r.account}</th><td>{r.debit_cents ? formatCents(r.debit_cents) : "—"}</td><td>{r.credit_cents ? formatCents(r.credit_cents) : "—"}</td></tr>)}</tbody>
   <tfoot><tr><th scope="row" colSpan={7}>Totals</th><td>{formatCents(data.debit_cents)}</td><td>{formatCents(data.credit_cents)}</td></tr></tfoot></table>
   {data.truncated && <p className="notice">Showing the first 5,000 lines.</p>}</div>}
 </ReportFrame>;
}
