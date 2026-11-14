"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Row = { code: string; name: string; in_cents: number; out_cents: number; net_cents: number };
type Data = { opening_cents: number; rows: Row[]; in_cents: number; out_cents: number; net_cents: number; closing_cents: number };
export default function CashFlowsDirect() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
 const [from, setFrom] = useState(""), [to, setTo] = useState("");
 const [data, setData] = useState<Data | null>(null), [previous, setPrevious] = useState<Data | null>(null);
 const [compare, setCompare] = useState(false), [showIn, setShowIn] = useState(true), [showOut, setShowOut] = useState(true), [showNet, setShowNet] = useState(true);
 const [busy, setBusy] = useState(false), [error, setError] = useState("");
 async function run(e: React.FormEvent) { e.preventDefault(); if (!orgId || busy) return; setData(null); setPrevious(null); setError(""); setBusy(true); try {
  setData(await api<Data>(`/v1/reports/cash-flows-direct?org_id=${orgId}&from=${from}&to=${to}`));
  if (compare) {
   const start = new Date(from + "T00:00:00Z"), end = new Date(to + "T00:00:00Z");
   const days = Math.round((end.getTime() - start.getTime()) / 86400000) + 1;
   const prevEnd = new Date(start.getTime() - 86400000), prevStart = new Date(prevEnd.getTime() - (days - 1) * 86400000);
   setPrevious(await api<Data>(`/v1/reports/cash-flows-direct?org_id=${orgId}&from=${prevStart.toISOString().slice(0, 10)}&to=${prevEnd.toISOString().slice(0, 10)}`));
  }
 } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); } }
 function exportCSV() {
  if (!data) return;
  const head = ["Account", ...(showIn ? ["In"] : []), ...(showOut ? ["Out"] : []), ...(showNet ? ["Net"] : []), ...(previous && compare ? ["Prior net"] : [])];
  const rows = data.rows.map(r => [r.code ? `${r.code} ${r.name}` : r.name, ...(showIn ? [r.in_cents/100] : []), ...(showOut ? [r.out_cents/100] : []), ...(showNet ? [r.net_cents/100] : []), ...(previous && compare ? [(previous.rows.find(p => p.code === r.code)?.net_cents ?? 0)/100] : [])]);
  const csv = [head, ...rows].map(r => r.map(v => `"${String(v)}"`).join(",")).join("\n");
  const url = URL.createObjectURL(new Blob([csv], { type: "text/csv" }));
  const a = document.createElement("a"); a.href = url; a.download = `cash-flows-direct-${from}-${to}.csv`; a.click(); URL.revokeObjectURL(url);
 }
 const filters = <form onSubmit={run} className="inline-form"><div><label htmlFor="cf-from">From</label><DateInput id="cf-from" required disabled={busy} value={from} onChange={e => { setFrom(e.target.value); setData(null); }}/></div><div><label htmlFor="cf-to">To</label><DateInput id="cf-to" required disabled={busy} value={to} onChange={e => { setTo(e.target.value); setData(null); }}/></div><label className="check-label"><input type="checkbox" checked={compare} onChange={e => setCompare(e.target.checked)} /> Compare previous period</label><details><summary>Columns</summary><label className="check-label"><input type="checkbox" checked={showIn} onChange={e => setShowIn(e.target.checked)} /> Cash in</label><label className="check-label"><input type="checkbox" checked={showOut} onChange={e => setShowOut(e.target.checked)} /> Cash out</label><label className="check-label"><input type="checkbox" checked={showNet} onChange={e => setShowNet(e.target.checked)} /> Net</label></details><button disabled={busy || !orgId}>{busy ? "Running…" : "Update"}</button><button type="button" className="button-secondary" disabled={!data} onClick={exportCSV}>Export CSV</button></form>;
 return <ReportFrame title="Statement of cash flows (direct)" description="Cash in and out by the account on the other side, up to 366 days." filters={filters}>
  <p>Cash means the asset accounts coded 1000–1099. Money moved between those accounts is not a cash flow. Journals naming several other accounts are grouped as Split entries. Compare adds the equal-length period before.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {data && <div className="table-scroll"><table className="doc-table"><caption>Cash flows (direct)</caption><thead><tr><th>Account</th>{showIn&&<th className="num">In</th>}{showOut&&<th className="num">Out</th>}{showNet&&<th className="num">Net</th>}{previous&&compare&&<th className="num">Prior net</th>}</tr></thead><tbody>
   <tr><th scope="row">Cash at beginning of period</th><td colSpan={(showIn?1:0)+(showOut?1:0)+(showNet?1:0)+(previous&&compare?1:0)} className="num">{formatCents(data.opening_cents)}</td></tr>
   {data.rows.map(r => <tr key={r.code || "split"}><th scope="row">{r.code ? `${r.code} ${r.name}` : r.name}</th>{showIn&&<td className="num">{formatCents(r.in_cents)}</td>}{showOut&&<td className="num">{formatCents(r.out_cents)}</td>}{showNet&&<td className="num">{formatCents(r.net_cents)}</td>}{previous&&compare&&<td className="num">{formatCents(previous.rows.find(p => p.code === r.code)?.net_cents ?? 0)}</td>}</tr>)}
   <tr><th scope="row">Net cash flows</th>{showIn&&<td className="num">{formatCents(data.in_cents)}</td>}{showOut&&<td className="num">{formatCents(data.out_cents)}</td>}{showNet&&<td className="num">{formatCents(data.net_cents)}</td>}{previous&&compare&&<td className="num">{formatCents(previous.net_cents)}</td>}</tr>
   <tr><th scope="row">Cash at end of period</th><td colSpan={(showIn?1:0)+(showOut?1:0)+(showNet?1:0)+(previous&&compare?1:0)} className="num">{formatCents(data.closing_cents)}</td></tr>
  </tbody></table></div>}
 </ReportFrame>;
}
