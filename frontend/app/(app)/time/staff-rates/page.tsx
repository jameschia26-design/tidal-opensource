"use client";
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { formatCents, parseCents } from "@/lib/money";

type Staff = { user_id: number; email: string; name: string; hourly_cents: number; change_count: number; last_changed_at: string | null };
type Change = { hourly_cents: number; at: string; changed_by: string };

export default function StaffRates() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}
function Workspace() {
  const { selected } = useOrg(); const orgId = selected?.id;
  const [staff, setStaff] = useState<Staff[]>([]); const [edit, setEdit] = useState<number | null>(null); const [value, setValue] = useState("");
  const [histFor, setHistFor] = useState<Staff | null>(null); const [hist, setHist] = useState<Change[]>([]);
  const [busy, setBusy] = useState(false); const [error, setError] = useState(""); const [notice, setNotice] = useState("");
  const canWrite = selected && ["owner", "admin", "accountant"].includes(selected.role);
  async function load(id: number) { setStaff((await api<{ staff: Staff[] }>(`/v1/staff-rates?org_id=${id}`)).staff); }
  useEffect(() => { if (orgId != null && canWrite) void load(orgId).catch((e: ApiError) => setError(messageFor(e.error, e.status))); }, [orgId, canWrite]);
  async function save(s: Staff) {
    const cents = parseCents(value);
    if (!orgId || cents === null || busy) { setError("Enter a rate such as 45.00."); return; }
    setBusy(true); setError(""); setNotice("");
    try { await api("/v1/staff-rates", { method: "POST", body: JSON.stringify({ org_id: orgId, user_id: s.user_id, hourly_cents: cents }) }); setEdit(null); setNotice("Rate saved."); await load(orgId); }
    catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } finally { setBusy(false); }
  }
  async function showHistory(s: Staff) {
    if (!orgId || busy) return;
    setBusy(true); setError("");
    try { setHist((await api<{ history: Change[] }>(`/v1/staff-rates/${s.user_id}/history?org_id=${orgId}`)).history); setHistFor(s); }
    catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } finally { setBusy(false); }
  }
  return <>
    <PageHeader title="Staff cost rates" description="Hourly cost per staff member in SGD, for project costing. Rates never post to the ledger." />
    {!selected && <p>Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}
    {notice && <p role="status">{notice}</p>}
    {selected && !canWrite && <p>Only company owners, admins and accountants can view staff cost rates.</p>}
    {canWrite && <div className="table-scroll"><table className="doc-table">
      <caption>Staff</caption>
      <thead><tr><th>Staff</th><th className="num">Hourly cost (SGD)</th><th className="num">Changes</th><th>Actions</th></tr></thead>
      <tbody>{staff.map(s => <tr key={s.user_id}>
        <th scope="row">{s.name ? `${s.name} (${s.email})` : s.email}</th>
        <td className="num">{edit === s.user_id
          ? <><label htmlFor={`rate-${s.user_id}`}>Rate for {s.email}</label><input id={`rate-${s.user_id}`} inputMode="decimal" value={value} onChange={e => setValue(e.target.value)} /></>
          : formatCents(s.hourly_cents)}</td>
        <td className="num">{s.change_count}{s.last_changed_at ? ` · ${s.last_changed_at}` : ""}</td>
        <td>{edit === s.user_id
          ? <><button disabled={busy} onClick={() => void save(s)}>Save</button>{" "}<button className="button-secondary" disabled={busy} onClick={() => setEdit(null)}>Cancel</button></>
          : <><button className="button-secondary" onClick={() => { setEdit(s.user_id); setValue((s.hourly_cents / 100).toFixed(2)); }}>Edit {s.email}</button>{" "}
            <button className="button-secondary" disabled={busy || s.change_count === 0} title={s.change_count === 0 ? "No saved changes yet" : "Show every saved rate for this staff member"} onClick={() => void showHistory(s)}>History</button></>}
        </td>
      </tr>)}</tbody>
    </table></div>}
    {canWrite && histFor && <div className="table-scroll"><table className="doc-table">
      <caption>Rate history for {histFor.name ? `${histFor.name} (${histFor.email})` : histFor.email}</caption>
      <thead><tr><th scope="col">Saved at</th><th scope="col" className="num">Hourly cost (SGD)</th><th scope="col">Saved by</th></tr></thead>
      <tbody>{hist.map((c, i) => <tr key={i}><td>{c.at}</td><td className="num">{formatCents(c.hourly_cents)}</td><td>{c.changed_by || "—"}</td></tr>)}</tbody>
    </table><p><button className="button-secondary" onClick={() => { setHistFor(null); setHist([]); }}>Close history</button></p></div>}
  </>;
}
