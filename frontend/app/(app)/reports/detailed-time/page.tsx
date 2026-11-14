"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";
import { formatDate } from "@/lib/dates";

type Entry = { id: number; project: string; project_status: string; date: string; task: string; description: string; minutes: number; staff: string; recorder_id: number; billable: boolean; rate_cents: number; value_cents: number };
const hours = (m: number) => `${Math.floor(m / 60)}h ${String(m % 60).padStart(2, "0")}m`;

export default function DetailedTime() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
  const [from, setFrom] = useState(""), [to, setTo] = useState("");
  const [rows, setRows] = useState<Entry[] | null>(null), [truncated, setTruncated] = useState(false), [busy, setBusy] = useState(false), [error, setError] = useState("");
  const [status, setStatus] = useState(""), [showTask, setShowTask] = useState(true), [showDescription, setShowDescription] = useState(true), [showBillable, setShowBillable] = useState(true), [showRate, setShowRate] = useState(true);
  async function run(e: React.FormEvent) {
    e.preventDefault();
    if (!orgId || busy) return;
    setRows(null); setError(""); setBusy(true);
    try {
      const data = await api<{ entries: Entry[]; truncated: boolean }>(`/v1/time-entries?org_id=${orgId}&from=${from}&to=${to}`);
      setRows(data.entries); setTruncated(data.truncated);
    } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); }
  }
  const filtered = (rows ?? []).filter(r => !status || r.project_status === status);
  const groups = new Map<number, Entry[]>();
  for (const r of filtered) groups.set(r.recorder_id, [...(groups.get(r.recorder_id) ?? []), r]);
  const statuses = [...new Set((rows ?? []).map(r => r.project_status))];
  function exportCSV() {
    const head = ["Staff", "Project", "Status", "Date", ...(showTask ? ["Task"] : []), ...(showDescription ? ["Description"] : []), "Time", ...(showBillable ? ["Billable", "Value"] : []), ...(showRate ? ["Rate"] : [])];
    const body = filtered.map(r => [r.staff, r.project, r.project_status, r.date, ...(showTask ? [r.task] : []), ...(showDescription ? [r.description] : []), hours(r.minutes), ...(showBillable ? [r.billable ? "Yes" : "No", (r.value_cents / 100).toFixed(2)] : []), ...(showRate ? [(r.rate_cents / 100).toFixed(2)] : [])]);
    const csv = [head, ...body].map(r => r.map(v => `"${String(v).replaceAll('"', '""')}"`).join(",")).join("\n");
    const url = URL.createObjectURL(new Blob([csv], { type: "text/csv" }));
    const a = document.createElement("a"); a.href = url; a.download = `detailed-time-${from}-${to}.csv`; a.click(); URL.revokeObjectURL(url);
  }
  const filters = <form onSubmit={run} className="inline-form"><div><label htmlFor="dt-from">From</label><DateInput id="dt-from" required disabled={busy} value={from} onChange={e => { setFrom(e.target.value); setRows(null); }}/></div><div><label htmlFor="dt-to">To</label><DateInput id="dt-to" required disabled={busy} value={to} onChange={e => { setTo(e.target.value); setRows(null); }}/></div><div><label htmlFor="dt-status">Project status</label><select id="dt-status" value={status} onChange={e => setStatus(e.target.value)}><option value="">All statuses</option>{statuses.map(s => <option key={s} value={s}>{s}</option>)}</select></div><details><summary>Columns</summary><label className="check-label"><input type="checkbox" checked={showTask} onChange={e => setShowTask(e.target.checked)} /> Task</label><label className="check-label"><input type="checkbox" checked={showDescription} onChange={e => setShowDescription(e.target.checked)} /> Description</label><label className="check-label"><input type="checkbox" checked={showBillable} onChange={e => setShowBillable(e.target.checked)} /> Billable and charge</label><label className="check-label"><input type="checkbox" checked={showRate} onChange={e => setShowRate(e.target.checked)} /> Rate</label></details><button disabled={busy || !orgId}>{busy ? "Running…" : "Update"}</button><button type="button" className="button-secondary" disabled={!rows} onClick={exportCSV}>Export CSV</button></form>;
  return <ReportFrame title="Detailed time" description="Time entries by date, grouped by the user who recorded them." filters={filters}>
    <p>Grouped by recorder, not by the staff member who did the work. Charge value is time at the entry&apos;s rate; costing is not recorded on time entries.</p>
    {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
    {rows && (filtered.length === 0 ? <p>Nothing to show here.</p> : [...groups].map(([recorderID, list]) => <div className="table-scroll" key={recorderID}><table className="doc-table"><caption>{list[0].staff} (user {recorderID})</caption><thead><tr><th>Date</th><th>Project</th><th>Status</th>{showTask&&<th>Task</th>}{showDescription&&<th>Description</th>}<th>Time</th>{showBillable&&<><th>Billable</th><th className="num">Charge</th></>}{showRate&&<th className="num">Rate</th>}</tr></thead><tbody>{list.map(r => <tr key={r.id}><td>{formatDate(r.date)}</td><td>{r.project}</td><td>{r.project_status}</td>{showTask&&<td>{r.task}</td>}{showDescription&&<td>{r.description}</td>}<td>{hours(r.minutes)}</td>{showBillable&&<><td>{r.billable ? "Yes" : "No"}</td><td className="num">{formatCents(r.value_cents)}</td></>}{showRate&&<td className="num">{formatCents(r.rate_cents)}</td>}</tr>)}</tbody><tfoot><tr><th scope="row" colSpan={3+(showTask?1:0)+(showDescription?1:0)}>Total</th><td>{hours(list.reduce((s, r) => s + r.minutes, 0))}</td>{showBillable&&<><td/><td className="num">{formatCents(list.reduce((s, r) => s + r.value_cents, 0))}</td></>}{showRate&&<td/>}</tr></tfoot></table></div>))}
    {rows && truncated && <p role="status">Showing first 500 entries. Narrow the date range.</p>}
  </ReportFrame>;
}
