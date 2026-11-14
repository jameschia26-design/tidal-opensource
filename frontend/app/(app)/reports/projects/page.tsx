"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Row = { project_id: number; project: string; contact: string; active: boolean; minutes: number; entries: number };
export default function ProjectsReport() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
 const [from, setFrom] = useState(""), [to, setTo] = useState(""), [status, setStatus] = useState("active");
 const [rows, setRows] = useState<Row[] | null>(null), [truncated, setTruncated] = useState(false), [busy, setBusy] = useState(false), [error, setError] = useState("");
 async function run(e: React.FormEvent) { e.preventDefault(); if (!orgId || busy) return; setRows(null); setError(""); setBusy(true); try { const data = await api<{ rows: Row[]; truncated: boolean }>(`/v1/reports/project-time?org_id=${orgId}&from=${from}&to=${to}&status=${status}`); setRows(data.rows); setTruncated(data.truncated); } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); } }
 const filters = <form onSubmit={run} className="inline-form"><div><label htmlFor="pj-status">Project status</label><select id="pj-status" disabled={busy} value={status} onChange={e => { setStatus(e.target.value); setRows(null); }}><option value="active">Active</option><option value="inactive">Inactive</option><option value="all">All</option></select></div><div><label htmlFor="pj-from">From</label><DateInput id="pj-from" required disabled={busy} value={from} onChange={e => { setFrom(e.target.value); setRows(null); }}/></div><div><label htmlFor="pj-to">To</label><DateInput id="pj-to" required disabled={busy} value={to} onChange={e => { setTo(e.target.value); setRows(null); }}/></div><button disabled={busy || !orgId}>{busy ? "Running…" : "Update"}</button></form>;
 function hours(m: number) { const h = Math.floor(m / 60), mm = m % 60; return `${h}h ${mm}m`; }
 return <ReportFrame title="Project time" description="Logged time per project, capped at 500 projects. Cost, revenue and gross profit are on Project Financials." filters={filters}>
   <p>This view totals logged time. Project Financials adds cost, revenue and gross profit; Export CSV, Columns and Group come from the report toolbar.</p>
   {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
   {rows && (rows.length === 0 ? <p>Nothing to show here.</p> : <div className="table-scroll"><table className="doc-table"><caption>Project time totals</caption><thead><tr><th>Project</th><th>Contact</th><th>Status</th><th>Logged</th><th>Entries</th></tr></thead><tbody>{rows.map(r => <tr key={r.project_id}><td>{r.project}</td><td>{r.contact}</td><td>{r.active ? "Active" : "Inactive"}</td><td>{hours(r.minutes)}</td><td>{r.entries}</td></tr>)}</tbody></table></div>)}
   {rows && truncated && <p role="status">Showing first 500 projects.</p>}
  </ReportFrame>;
}
