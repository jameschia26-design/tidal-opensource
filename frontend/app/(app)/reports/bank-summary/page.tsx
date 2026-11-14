"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Row = { code: string; name: string; type: string; status: string; opening_cents: number; received_cents: number; spent_cents: number; closing_cents: number };
type Data = { rows: Row[]; total: Row };
export default function BankSummary() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
 const [from, setFrom] = useState(""), [to, setTo] = useState("");
 const [data, setData] = useState<Data | null>(null), [busy, setBusy] = useState(false), [error, setError] = useState("");
 const [showType, setShowType] = useState(true), [showStatus, setShowStatus] = useState(true);
 async function run(e: React.FormEvent) { e.preventDefault(); if (!orgId || busy) return; setData(null); setError(""); setBusy(true); try { setData(await api<Data>(`/v1/reports/bank-summary?org_id=${orgId}&from=${from}&to=${to}`)); } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); } }
 const filters = <form onSubmit={run} className="inline-form"><div><label htmlFor="bs-from">From</label><DateInput id="bs-from" required disabled={busy} value={from} onChange={e => { setFrom(e.target.value); setData(null); }}/></div><div><label htmlFor="bs-to">To</label><DateInput id="bs-to" required disabled={busy} value={to} onChange={e => { setTo(e.target.value); setData(null); }}/></div><details><summary>Columns</summary><label className="check-label"><input type="checkbox" checked={showType} onChange={e => setShowType(e.target.checked)} /> Account type</label><label className="check-label"><input type="checkbox" checked={showStatus} onChange={e => setShowStatus(e.target.checked)} /> Status</label></details><button type="button" className="button-secondary" disabled={!data} onClick={() => { if (!data) return; const head = ["Code","Account",...(showType?["Account type"]:[]),...(showStatus?["Status"]:[]),"Opening","Received","Spent","Closing"]; const body = data.rows.map(r => [r.code, r.name, ...(showType?[r.type]:[]), ...(showStatus?[r.status]:[]), (r.opening_cents/100).toFixed(2), (r.received_cents/100).toFixed(2), (r.spent_cents/100).toFixed(2), (r.closing_cents/100).toFixed(2)]); const csv = [head, ...body].map(r => r.map(v => `"${String(v).replaceAll('"','""')}"`).join(",")).join("\n"); const url = URL.createObjectURL(new Blob([csv], { type: "text/csv" })); const al = document.createElement("a"); al.href = url; al.download = `bank-summary-${from}-${to}.csv`; al.click(); URL.revokeObjectURL(url); }}>Export CSV</button><button disabled={busy || !orgId}>{busy ? "Running…" : "Update"}</button></form>;
 return <ReportFrame title="Bank summary" description="Movement on every bank account, up to 366 days." filters={filters}>
  <p>Bank accounts are the accounts flagged as bank accounts in the chart of accounts, whatever their code. Posted journals only. Use the column chooser to hide type or status, and export for the spreadsheet.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {data && <div className="table-scroll"><table className="doc-table"><caption>Bank summary</caption><thead><tr><th>Code</th><th>Account</th>{showType&&<th>Account type</th>}{showStatus&&<th>Status</th>}<th className="num">Opening</th><th className="num">Received</th><th className="num">Spent</th><th className="num">Closing</th></tr></thead>
   <tbody>{data.rows.map(r => <tr key={r.code}><td>{r.code}</td><td>{r.name}</td>{showType&&<td>{r.type}</td>}{showStatus&&<td>{r.status}</td>}<td className="num">{formatCents(r.opening_cents)}</td><td className="num">{formatCents(r.received_cents)}</td><td className="num">{formatCents(r.spent_cents)}</td><td className="num">{formatCents(r.closing_cents)}</td></tr>)}
   <tr><th colSpan={2 + (showType?1:0) + (showStatus?1:0)} scope="row">Total</th><td className="num">{formatCents(data.total.opening_cents)}</td><td className="num">{formatCents(data.total.received_cents)}</td><td className="num">{formatCents(data.total.spent_cents)}</td><td className="num">{formatCents(data.total.closing_cents)}</td></tr></tbody></table></div>}
 </ReportFrame>;
}
