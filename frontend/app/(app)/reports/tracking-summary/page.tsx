"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

const formatBalance = (n: number) => (n === 0 ? "" : n < 0 ? `(${formatCents(-n)})` : formatCents(n));
type Row = { category: string; option: string; income_cents: number; expense_cents: number; opening_cents: number; net_cents: number; closing_cents: number };
type Data = { rows: Row[]; untracked_income_cents: number; untracked_expense_cents: number; untracked_opening_cents: number; untracked_net_cents: number; untracked_closing_cents: number; income_cents: number; expense_cents: number; opening_cents: number; net_cents: number; closing_cents: number };
export default function TrackingSummary() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
 const [from, setFrom] = useState(""), [to, setTo] = useState("");
 const [data, setData] = useState<Data | null>(null), [busy, setBusy] = useState(false), [error, setError] = useState("");
 async function run(e: React.FormEvent) { e.preventDefault(); if (!orgId || busy) return; setData(null); setError(""); setBusy(true); try { setData(await api<Data>(`/v1/reports/tracking-summary?org_id=${orgId}&from=${from}&to=${to}`)); } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); } }
 function exportCSV() { if (!data) return; const head=["Category","Option","Income","Expenses","Opening balance","Net movement","Closing balance"]; const rows=data.rows.map(r=>[r.category,r.option,r.income_cents/100,r.expense_cents/100,r.opening_cents/100,r.net_cents/100,r.closing_cents/100]); rows.push(["","Untracked",data.untracked_income_cents/100,data.untracked_expense_cents/100,data.untracked_opening_cents/100,data.untracked_net_cents/100,data.untracked_closing_cents/100]); rows.push(["","Total",data.income_cents/100,data.expense_cents/100,data.opening_cents/100,data.net_cents/100,data.closing_cents/100]); const csv=[head,...rows].map(r=>r.map(v=>`"${v}"`).join(",")).join("\n"); const url=URL.createObjectURL(new Blob([csv],{type:"text/csv"})); const a=document.createElement("a"); a.href=url; a.download=`tracking-summary-${from}-${to}.csv`; a.click(); URL.revokeObjectURL(url); }
 const filters = <form onSubmit={run} className="inline-form"><div><label htmlFor="ts-from">From</label><DateInput id="ts-from" required disabled={busy} value={from} onChange={e => { setFrom(e.target.value); setData(null); }}/></div><div><label htmlFor="ts-to">To</label><DateInput id="ts-to" required disabled={busy} value={to} onChange={e => { setTo(e.target.value); setData(null); }}/></div><button disabled={busy || !orgId}>{busy ? "Running…" : "Update"}</button><button type="button" className="button-secondary" disabled={!data} onClick={exportCSV}>Export CSV</button></form>;
 return <ReportFrame title="Tracking summary" description="Posted invoices and bills by reporting tag option, up to 366 days." filters={filters}>
  <p>Amounts come from the posted journal lines of profit and loss and balance sheet accounts, split by the tracking option on each line, so one document can split across options. Lines without an option are reported as untracked, and the totals reconcile to the profit and loss for the same dates. Opening, net movement and closing cover the balance sheet accounts only and are debit positive, so a liability shows negative; P&amp;L rows leave those columns empty. Export copies every category and the totals.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {data && (data.rows.length === 0 ? <p>Nothing to show here.</p> : <div className="table-scroll"><table className="doc-table"><caption>Tracking summary</caption><thead><tr><th>Category</th><th>Option</th><th className="num">Income</th><th className="num">Expenses</th><th className="num">Opening balance</th><th className="num">Net movement</th><th className="num">Closing balance</th></tr></thead>
   <tbody>{data.rows.map((r, i) => <tr key={i}><td>{r.category}</td><td>{r.option}</td><td className="num">{formatCents(r.income_cents)}</td><td className="num">{formatCents(r.expense_cents)}</td><td className="num">{formatBalance(r.opening_cents)}</td><td className="num">{formatBalance(r.net_cents)}</td><td className="num">{formatBalance(r.closing_cents)}</td></tr>)}
   <tr><th colSpan={2} scope="row">Untracked</th><td className="num">{formatCents(data.untracked_income_cents)}</td><td className="num">{formatCents(data.untracked_expense_cents)}</td><td className="num">{formatBalance(data.untracked_opening_cents)}</td><td className="num">{formatBalance(data.untracked_net_cents)}</td><td className="num">{formatBalance(data.untracked_closing_cents)}</td></tr>
   <tr><th colSpan={2} scope="row">Total</th><td className="num">{formatCents(data.income_cents)}</td><td className="num">{formatCents(data.expense_cents)}</td><td className="num">{formatBalance(data.opening_cents)}</td><td className="num">{formatBalance(data.net_cents)}</td><td className="num">{formatBalance(data.closing_cents)}</td></tr></tbody></table></div>)}
 </ReportFrame>;
}
