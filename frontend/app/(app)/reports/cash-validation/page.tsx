"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Row = { contact_id: number; customer: string; invoiced_cents: number; paid_cents: number; credits_cents: number; balance_cents: number };
export default function CashValidation() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
 const [asOf, setAsOf] = useState("");
 const [data, setData] = useState<{ rows: Row[]; invoiced_cents: number; paid_cents: number; credits_cents: number; balance_cents: number } | null>(null);
 const [busy, setBusy] = useState(false), [error, setError] = useState("");
 async function run(e: React.FormEvent) {
  e.preventDefault(); if (!orgId || busy) return; setData(null); setError(""); setBusy(true);
  try { setData(await api(`/v1/reports/cash-validation?org_id=${orgId}&as_of=${asOf}`)); }
  catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); }
 }
 const filters = <form onSubmit={run} className="inline-form"><div><label htmlFor="cv-asof">As of</label><DateInput id="cv-asof" required disabled={busy} value={asOf} onChange={e => { setAsOf(e.target.value); setData(null); }}/></div><button disabled={busy || !orgId}>{busy ? "Running…" : "Update"}</button></form>;
 return <ReportFrame title="Cash validation" description="Customer invoices against cash received and credit notes, as a standing check." filters={filters}>
  <p>One row per customer with posted invoices to the date. A balance other than zero means documents and settlements disagree; posting guards should keep every balance matching its open documents. No account picker, column chooser, grouping or export.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {data && <div className="table-scroll"><table className="doc-table"><caption>Cash validation</caption><thead><tr><th>Customer</th><th>Invoiced</th><th>Paid</th><th>Credits</th><th>Balance</th></tr></thead><tbody>
   {data.rows.map(r => <tr key={r.contact_id}><th scope="row">{r.customer}</th><td>{formatCents(r.invoiced_cents)}</td><td>{formatCents(r.paid_cents)}</td><td>{formatCents(r.credits_cents)}</td><td>{formatCents(r.balance_cents)}</td></tr>)}</tbody>
   <tfoot><tr><th scope="row">Total</th><td>{formatCents(data.invoiced_cents)}</td><td>{formatCents(data.paid_cents)}</td><td>{formatCents(data.credits_cents)}</td><td>{formatCents(data.balance_cents)}</td></tr></tfoot></table>
   {data.rows.length === 0 && <p className="notice">No posted customer invoices to this date.</p>}</div>}
 </ReportFrame>;
}
