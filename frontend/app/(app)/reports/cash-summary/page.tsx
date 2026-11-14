"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Totals = { opening_cents: number; received_cents: number; spent_cents: number; net_cents: number; closing_cents: number };
type Month = { month: string; received_cents: number; spent_cents: number; net_cents: number; closing_cents: number };
type Cash = Totals & { monthly: Month[]; average_net_cents: number; previous: Totals & { from: string; to: string } };
const columns = [["received", "Received"], ["spent", "Spent"], ["net", "Net"], ["closing", "Closing"]] as const;
export default function CashSummary() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
 const [from, setFrom] = useState(""), [to, setTo] = useState("");
 const [data, setData] = useState<Cash | null>(null), [shown, setShown] = useState<string[]>(["received", "spent", "net", "closing"]);
 const [busy, setBusy] = useState(false), [error, setError] = useState("");
 async function run(e: React.FormEvent) { e.preventDefault(); if (!orgId || busy) return; setData(null); setError(""); setBusy(true); try { setData(await api<Cash>(`/v1/reports/cash-summary?org_id=${orgId}&from=${from}&to=${to}`)); } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); } }
 function exportCSV() {
  if (!data) return;
  const head = ["Month", ...columns.filter(([k]) => shown.includes(k)).map(([, label]) => label)];
  const rows = data.monthly.map(m => [m.month, ...columns.filter(([k]) => shown.includes(k)).map(([k]) => ((m[(k + "_cents") as keyof Month] as number) / 100).toFixed(2))]);
  const csv = [head, ...rows].map(r => r.map(v => `"${String(v)}"`).join(",")).join("\n");
  const url = URL.createObjectURL(new Blob([csv], { type: "text/csv" }));
  const a = document.createElement("a"); a.href = url; a.download = `cash-summary-${from}-${to}.csv`; a.click(); URL.revokeObjectURL(url);
 }
 const filters = <form onSubmit={run} className="inline-form"><div><label htmlFor="cs-from">From</label><DateInput id="cs-from" required disabled={busy} value={from} onChange={e => { setFrom(e.target.value); setData(null); }}/></div><div><label htmlFor="cs-to">To</label><DateInput id="cs-to" required disabled={busy} value={to} onChange={e => { setTo(e.target.value); setData(null); }}/></div><details><summary>Columns</summary>{columns.map(([k, label]) => <label key={k} className="check-label"><input type="checkbox" checked={shown.includes(k)} onChange={e => setShown(e.target.checked ? [...shown, k] : shown.filter(v => v !== k))} /> {label}</label>)}</details><button disabled={busy || !orgId}>{busy ? "Running…" : "Update"}</button><button type="button" className="button-secondary" disabled={!data} onClick={exportCSV}>Export CSV</button></form>;
 return <ReportFrame title="Cash summary" description="Movement on the Bank account, up to 366 days." filters={filters}>
  <p>Uses the standard Bank account (1000) and posted journals only. The table splits the period by month, adds the average monthly net movement and compares the equal-length period before it.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {data && <>
   <div className="table-scroll"><table className="doc-table"><caption>Cash summary</caption><thead><tr><th>Measure</th><th className="num">This period</th><th className="num">Previous</th><th className="num">Variance</th></tr></thead><tbody>
    <tr><th scope="row">Opening balance</th><td className="num">{formatCents(data.opening_cents)}</td><td className="num">{formatCents(data.previous.opening_cents)}</td><td className="num">{formatCents(data.opening_cents - data.previous.opening_cents)}</td></tr>
    {shown.includes("received") && <tr><th scope="row">Cash received</th><td className="num">{formatCents(data.received_cents)}</td><td className="num">{formatCents(data.previous.received_cents)}</td><td className="num">{formatCents(data.received_cents - data.previous.received_cents)}</td></tr>}
    {shown.includes("spent") && <tr><th scope="row">Cash spent</th><td className="num">{formatCents(data.spent_cents)}</td><td className="num">{formatCents(data.previous.spent_cents)}</td><td className="num">{formatCents(data.spent_cents - data.previous.spent_cents)}</td></tr>}
    {shown.includes("net") && <tr><th scope="row">Net cash movement</th><td className="num">{formatCents(data.net_cents)}</td><td className="num">{formatCents(data.previous.net_cents)}</td><td className="num">{formatCents(data.net_cents - data.previous.net_cents)}</td></tr>}
    {shown.includes("closing") && <tr><th scope="row">Closing balance</th><td className="num">{formatCents(data.closing_cents)}</td><td className="num">{formatCents(data.previous.closing_cents)}</td><td className="num">{formatCents(data.closing_cents - data.previous.closing_cents)}</td></tr>}
   </tbody></table></div>
   <div className="table-scroll"><table className="doc-table"><caption>Monthly movement</caption><thead><tr><th>Month</th>{columns.filter(([k]) => shown.includes(k)).map(([, label]) => <th key={label} className="num">{label}</th>)}</tr></thead><tbody>
    {data.monthly.map(m => <tr key={m.month}><th scope="row">{m.month}</th>{columns.filter(([k]) => shown.includes(k)).map(([k]) => <td key={k} className="num">{formatCents(m[(k + "_cents") as keyof Month] as number)}</td>)}</tr>)}
    <tr><th scope="row">Average per month</th><td colSpan={shown.length} className="num">{formatCents(data.average_net_cents)}</td></tr>
   </tbody></table></div>
  </>}
 </ReportFrame>;
}
