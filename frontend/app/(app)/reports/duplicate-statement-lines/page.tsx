"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";
import { formatDate } from "@/lib/dates";

type Line = { id: number; date: string; description: string; reference: string; amount_cents: number; matched: boolean };
export default function DuplicateStatementLines() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
 const [from, setFrom] = useState(""), [to, setTo] = useState("");
 const [rows, setRows] = useState<Line[] | null>(null), [truncated, setTruncated] = useState(false), [busy, setBusy] = useState(false), [error, setError] = useState("");
 async function run(e: React.FormEvent) { e.preventDefault(); if (!orgId || busy) return; setRows(null); setError(""); setBusy(true); try { const d = await api<{ lines: Line[]; truncated: boolean }>(`/v1/reports/duplicate-statement-lines?org_id=${orgId}&from=${from}&to=${to}`); setRows(d.lines); setTruncated(d.truncated); } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); } }
 const filters = <form onSubmit={run} className="inline-form"><div><label htmlFor="ds-from">From</label><DateInput id="ds-from" required disabled={busy} value={from} onChange={e => { setFrom(e.target.value); setRows(null); }}/></div><div><label htmlFor="ds-to">To</label><DateInput id="ds-to" required disabled={busy} value={to} onChange={e => { setTo(e.target.value); setRows(null); }}/></div><button disabled={busy || !orgId}>{busy ? "Running…" : "Update"}</button></form>;
 return <ReportFrame title="Duplicate statement lines" description="Imported statement lines sharing a date and amount, up to 366 days." filters={filters}>
  <p>Identical lines are skipped on import, so these differ in text or reference and may be genuine. Statements are not tied to a bank account, so all imported lines are compared.</p>
  {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
  {rows && (rows.length === 0 ? <p>Nothing to show here.</p> : <div className="table-scroll"><table className="doc-table"><caption>Possible duplicate lines</caption><thead><tr><th>Date</th><th>Description</th><th>Reference</th><th>Amount</th><th>Matched</th></tr></thead><tbody>{rows.map(r => <tr key={r.id}><td>{formatDate(r.date)}</td><td>{r.description}</td><td>{r.reference}</td><td>{formatCents(r.amount_cents)}</td><td>{r.matched ? "Yes" : "No"}</td></tr>)}</tbody></table></div>)}
  {rows && truncated && <p role="status">Showing first 500 lines. Narrow the date range.</p>}
 </ReportFrame>;
}
