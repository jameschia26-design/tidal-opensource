"use client";
import { useState, type FormEvent } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";
import { todayISO } from "@/lib/dates";

type Row = { item_id: number | null; item: string; quantity: number; sales_cents: number };
type Data = { from: string; to: string; rows: Row[] };
export default function SalesByItem() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
  const [from, setFrom] = useState(() => `${todayISO().slice(0, 4)}-01-01`), [to, setTo] = useState(todayISO);
  const [data, setData] = useState<Data | null>(null), [error, setError] = useState(""), [busy, setBusy] = useState(false);
  async function run(e: FormEvent) { e.preventDefault(); if (!orgId || busy) return; setBusy(true); setError(""); setData(null); try { setData(await api<Data>(`/v1/reports/sales-by-item?org_id=${orgId}&from=${from}&to=${to}`)); } catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } finally { setBusy(false); } }
  const filters = <form onSubmit={run} className="inline-form"><div><label htmlFor="sbi-from">From</label><DateInput id="sbi-from" required value={from} max={to} disabled={busy} onChange={e => setFrom(e.target.value)} /></div><div><label htmlFor="sbi-to">To</label><DateInput id="sbi-to" required value={to} min={from} disabled={busy} onChange={e => setTo(e.target.value)} /></div><button disabled={!orgId || busy}>{busy ? "Running…" : "Update"}</button></form>;
  return <ReportFrame title="Sales by item" description="Posted invoice, sales receipt and customer credit note lines, grouped by item and document date." filters={filters}>
    {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
    {data && (data.rows.length ? <div className="table-scroll"><table className="doc-table"><caption>Sales by item, {data.from} to {data.to}</caption><thead><tr><th>Item</th><th className="num">Quantity</th><th className="num">Net sales</th></tr></thead><tbody>{data.rows.map((r, i) => <tr key={`${r.item_id ?? "other"}-${i}`}><td>{r.item}</td><td className="num">{r.quantity}</td><td className="num">{formatCents(r.sales_cents)}</td></tr>)}</tbody></table></div> : <p>Nothing to show here.</p>)}
  </ReportFrame>;
}
