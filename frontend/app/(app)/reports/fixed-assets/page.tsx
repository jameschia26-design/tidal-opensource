"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";
import { formatDate } from "@/lib/dates";

type Row = { id: number; name: string; acquired_on: string; cost_cents: number; opening_accum_cents: number; period_dep_cents: number; closing_accum_cents: number; opening_book_cents: number; closing_book_cents: number };

export default function FixedAssetsReport() { const orgId = useOrgId(); return <Workspace key={orgId ?? "none"} orgId={orgId} />; }
function Workspace({ orgId }: { orgId: number | null }) {
  const [from, setFrom] = useState(""), [to, setTo] = useState("");
  const [rows, setRows] = useState<Row[] | null>(null), [truncated, setTruncated] = useState(false), [busy, setBusy] = useState(false), [error, setError] = useState("");
  async function run(e: React.FormEvent) {
    e.preventDefault();
    if (!orgId || busy) return;
    setRows(null); setError(""); setBusy(true);
    try {
      const data = await api<{ rows: Row[]; truncated: boolean }>(`/v1/reports/fixed-assets?org_id=${orgId}&from=${from}&to=${to}`);
      setRows(data.rows); setTruncated(data.truncated);
    } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); }
  }
  const sum = (f: (r: Row) => number) => (rows ?? []).reduce((s, r) => s + f(r), 0);
  const filters = <form onSubmit={run} className="inline-form"><div><label htmlFor="fa-from">From</label><DateInput id="fa-from" required disabled={busy} value={from} onChange={e => { setFrom(e.target.value); setRows(null); }}/></div><div><label htmlFor="fa-to">To</label><DateInput id="fa-to" required disabled={busy} value={to} onChange={e => { setTo(e.target.value); setRows(null); }}/></div><button disabled={busy || !orgId}>{busy ? "Running…" : "Update"}</button></form>;
  return <ReportFrame title="Depreciation schedule" description="Register cost, depreciation charged and book value by asset, up to 366 days." filters={filters}>
    <p>Straight-line book depreciation recorded in the register. Charges use their posted month.</p>
    {!orgId && <p>Choose a company first.</p>}{error && <p role="alert">{error}</p>}
    {rows && (rows.length === 0 ? <p>Nothing to show here.</p> : <div className="table-scroll"><table className="doc-table"><caption>Assets</caption><thead><tr><th>Asset</th><th>Acquired</th><th>Cost</th><th>Opening accumulated</th><th>Depreciation</th><th>Closing accumulated</th><th>Opening book value</th><th>Closing book value</th></tr></thead><tbody>{rows.map(r => <tr key={r.id}><th scope="row">{r.name}</th><td>{formatDate(r.acquired_on)}</td><td>{formatCents(r.cost_cents)}</td><td>{formatCents(r.opening_accum_cents)}</td><td>{formatCents(r.period_dep_cents)}</td><td>{formatCents(r.closing_accum_cents)}</td><td>{formatCents(r.opening_book_cents)}</td><td>{formatCents(r.closing_book_cents)}</td></tr>)}</tbody><tfoot><tr><th scope="row" colSpan={2}>{truncated ? "Shown assets total" : "Total"}</th><td>{formatCents(sum(r => r.cost_cents))}</td><td>{formatCents(sum(r => r.opening_accum_cents))}</td><td>{formatCents(sum(r => r.period_dep_cents))}</td><td>{formatCents(sum(r => r.closing_accum_cents))}</td><td>{formatCents(sum(r => r.opening_book_cents))}</td><td>{formatCents(sum(r => r.closing_book_cents))}</td></tr></tfoot></table></div>)}
    {rows && truncated && <p role="status">Showing first 500 assets.</p>}
  </ReportFrame>;
}
