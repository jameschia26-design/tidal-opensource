"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Row = { code: string; name: string; opening_cents: number; movement_cents: number; closing_cents: number };
type Data = { rows: Row[]; opening_cents: number; closing_cents: number };
const columns = [["opening", "Opening"], ["movement", "Movement"], ["closing", "Closing"]] as const;
export default function MovementsInEquity() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
 const [from, setFrom] = useState(""), [to, setTo] = useState("");
 const [data, setData] = useState<Data | null>(null), [previous, setPrevious] = useState<Data | null>(null);
 const [compare, setCompare] = useState(false), [shown, setShown] = useState<string[]>(["opening", "movement", "closing"]);
 const [busy, setBusy] = useState(false), [error, setError] = useState("");
 async function run(e: React.FormEvent) {
  e.preventDefault(); if (!orgId || busy) return; setData(null); setPrevious(null); setError(""); setBusy(true);
  try {
   const current = await api<Data>(`/v1/reports/movements-in-equity?org_id=${orgId}&from=${from}&to=${to}`);
   setData(current);
   if (compare) {
    const start = new Date(from + "T00:00:00Z"), end = new Date(to + "T00:00:00Z");
    const days = Math.round((end.getTime() - start.getTime()) / 86400000) + 1;
    const prevEnd = new Date(start.getTime() - 86400000), prevStart = new Date(prevEnd.getTime() - (days - 1) * 86400000);
    setPrevious(await api<Data>(`/v1/reports/movements-in-equity?org_id=${orgId}&from=${prevStart.toISOString().slice(0, 10)}&to=${prevEnd.toISOString().slice(0, 10)}`));
   }
  } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); }
 }
 function exportCSV() {
  if (!data) return;
  const head = ["Code", "Account", ...columns.filter(([k]) => shown.includes(k)).map(([, label]) => label)];
  const rows = data.rows.map(r => [r.code, r.name, ...columns.filter(([k]) => shown.includes(k)).map(([k]) => (r[k + "_cents" as keyof Row] as number / 100).toFixed(2))]);
  const csv = [head, ...rows].map(r => r.map(v => `"${String(v).replaceAll('"', '""')}"`).join(",")).join("\n");
  const url = URL.createObjectURL(new Blob([csv], { type: "text/csv" }));
  const a = document.createElement("a"); a.href = url; a.download = `movements-in-equity-${from}-${to}.csv`; a.click(); URL.revokeObjectURL(url);
 }
 const filters = <form onSubmit={run} className="inline-form"><div><label htmlFor="me-from">From</label><DateInput id="me-from" required disabled={busy} value={from} onChange={e => { setFrom(e.target.value); setData(null); }}/></div><div><label htmlFor="me-to">To</label><DateInput id="me-to" required disabled={busy} value={to} onChange={e => { setTo(e.target.value); setData(null); }}/></div><label className="check-label"><input type="checkbox" checked={compare} onChange={e => setCompare(e.target.checked)} /> Compare previous period</label><details><summary>Columns</summary>{columns.map(([k, label]) => <label key={k} className="check-label"><input type="checkbox" checked={shown.includes(k)} onChange={e => setShown(e.target.checked ? [...shown, k] : shown.filter(v => v !== k))} /> {label}</label>)}</details><button disabled={busy || !orgId}>{busy ? "Running…" : "Update"}</button><button type="button" className="button-secondary" disabled={!data} onClick={exportCSV}>Export CSV</button></form>;
 return <ReportFrame title="Movements in equity" description="Opening balance, movement and closing balance per equity account, up to 366 days." filters={filters}>
  <p>Posted journals only. Accumulated profit is not shown as an equity line; use the balance sheet for that. Compare adds the equal-length period before the chosen dates.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {data && <div className="table-scroll"><table className="doc-table"><caption>Movements in equity</caption><thead><tr><th>Code</th><th>Account</th>{columns.filter(([k]) => shown.includes(k)).map(([, label]) => <th key={label} className="num">{label}</th>)}{previous && compare && <th className="num">Previous closing</th>}</tr></thead>
   <tbody>{data.rows.map(r => <tr key={r.code}><td>{r.code}</td><td>{r.name}</td>{columns.filter(([k]) => shown.includes(k)).map(([k]) => <td key={k} className="num">{formatCents(r[(k + "_cents") as keyof Row] as number)}</td>)}{previous && compare && <td className="num">{formatCents(previous.rows.find(p => p.code === r.code)?.closing_cents ?? 0)}</td>}</tr>)}
   <tr><th colSpan={2} scope="row">Total equity</th>{shown.includes("opening") && <td className="num">{formatCents(data.opening_cents)}</td>}{shown.includes("movement") && <td />}{shown.includes("closing") && <td className="num">{formatCents(data.closing_cents)}</td>}{previous && compare && <td className="num">{formatCents(previous.closing_cents)}</td>}</tr></tbody></table></div>}
 </ReportFrame>;
}
