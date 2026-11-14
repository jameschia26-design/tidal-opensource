"use client";
import { useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { formatCents } from "@/lib/money";
import DateInput from "@/components/DateInput";
import { todayISO } from "@/lib/dates";

type Row = { contact_id: number; customer: string; invoices_cents: number; receipts_cents: number; credits_cents: number; net_cents: number };
type Report = { from: string; to: string; rows: Row[] };
const today = todayISO;
export default function SalesReport() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}
function Workspace() {
  const { selected } = useOrg();
  const [from, setFrom] = useState(() => `${today().slice(0, 4)}-01-01`);
  const [to, setTo] = useState(today);
  const [report, setReport] = useState<Report | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  async function run(e: FormEvent) {
    e.preventDefault();
    if (!selected || busy) return;
    setBusy(true); setError(""); setReport(null);
    try { setReport(await api<Report>(`/v1/reports/sales-by-customer?org_id=${selected.id}&from=${from}&to=${to}`)); }
    catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); }
    finally { setBusy(false); }
  }
  return <>
    <PageHeader title="Sales by customer" description="Posted invoices and sales receipts, less posted credit notes, by document date." />
    <p className="notice">This is a document-total report, not cash received or account revenue. Credit notes are shown in the period they are dated, including credits against older sales.</p>
    {!selected && <p>Choose a company first.</p>}{error && <p role="alert" className="form-error">{error}</p>}
    {selected && <form className="record-form" onSubmit={run}><fieldset disabled={busy}><legend>Report period</legend><div className="form-grid"><div><label htmlFor="sales-from">From</label><DateInput id="sales-from" required value={from} max={to} onChange={e => { setFrom(e.target.value); setReport(null); }}/></div><div><label htmlFor="sales-to">To</label><DateInput id="sales-to" required value={to} min={from} onChange={e => { setTo(e.target.value); setReport(null); }}/></div></div><button>{busy ? "Running…" : "Run report"}</button></fieldset></form>}
    {report && <section><h2>Sales from {report.from} to {report.to}</h2>{report.rows.length === 0 ? <p>No sales or credits in this period.</p> : <div className="table-scroll"><table className="doc-table"><caption>Sales by customer</caption><thead><tr><th>Customer</th><th className="num">Invoices</th><th className="num">Sales receipts</th><th className="num">Credits</th><th className="num">Net sales</th></tr></thead><tbody>{report.rows.map(r => <tr key={r.contact_id}><td>{r.customer}</td><td className="num">{formatCents(r.invoices_cents)}</td><td className="num">{formatCents(r.receipts_cents)}</td><td className="num">{formatCents(r.credits_cents)}</td><td className="num">{formatCents(r.net_cents)}</td></tr>)}</tbody></table></div>}</section>}
  </>;
}
