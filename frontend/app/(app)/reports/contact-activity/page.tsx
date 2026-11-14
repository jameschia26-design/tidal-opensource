"use client";
import { useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Row = { contact_id: number; contact: string; income_cents: number; expense_cents: number };
export default function ContactActivity() {
  const { selected } = useOrg();
  return <Workspace key={selected?.id ?? "none"} orgId={selected?.id} />;
}
function Workspace({ orgId }: { orgId: number | undefined }) {
  const [from,setFrom]=useState(""); const [to,setTo]=useState(""); const [rows,setRows]=useState<Row[] | null>(null); const [error,setError]=useState(""); const [busy,setBusy]=useState(false);
  async function run(e: FormEvent) {
    e.preventDefault(); if (!orgId || busy) return; setBusy(true); setError(""); setRows(null);
    try { setRows((await api<{rows: Row[]}>(`/v1/reports/contact-activity?org_id=${orgId}&from=${from}&to=${to}`)).rows); }
    catch (e) { const err=e as ApiError; setError(messageFor(err.error,err.status)); } finally { setBusy(false); }
  }
  return <><PageHeader title="Income and expenses by contact" description="Document totals by contact for the selected period." />
    <p>Partial report: posted invoices, sales receipts, credit notes and supplier bills only. Amounts include GST where applicable. Expenses without contact links, journals and other activity are excluded; this is not a ledger profit report.</p>
    <form onSubmit={run} className="inline-form"><div><label htmlFor="contact-from">From</label><DateInput id="contact-from" required disabled={busy} value={from} onChange={e=>{setFrom(e.target.value);setRows(null);}}/></div><div><label htmlFor="contact-to">To</label><DateInput id="contact-to" required disabled={busy} value={to} onChange={e=>{setTo(e.target.value);setRows(null);}}/></div><button disabled={!orgId || busy}>{busy ? "Running…" : "Run report"}</button></form>
    {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
    {rows && (rows.length ? <div className="table-scroll"><table className="doc-table"><thead><tr><th>Contact</th><th>Income documents</th><th>Supplier bills</th></tr></thead><tbody>{rows.map(row=><tr key={row.contact_id}><th scope="row">{row.contact}</th><td>{formatCents(row.income_cents)}</td><td>{formatCents(row.expense_cents)}</td></tr>)}</tbody></table></div> : <p>No activity in this period.</p>)}
  </>;
}
