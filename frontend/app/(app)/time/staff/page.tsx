"use client";
// "Staff time overview": hours per person for a period, with the projects they
// worked on. Reads the same time-entry rows as the time-entry screen.
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Entry = { id: number; project: string; date: string; minutes: number; staff: string };

export default function StaffTime() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}`} />;
}

function Workspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const [entries, setEntries] = useState<Entry[]>([]);
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [error, setError] = useState("");
  useEffect(() => {
    if (!orgId) return;
    let live = true;
    const q = new URLSearchParams({ org_id: String(orgId) });
    if (from && to) { q.set("from", from); q.set("to", to); }
    api<{ entries: Entry[] }>(`/v1/time-entries?${q}`)
      .then((r) => { if (live) { setEntries(r.entries); setError(""); } })
      .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [orgId, from, to]);

  const people = new Map<string, { minutes: number; projects: Map<string, number> }>();
  for (const e of entries) {
    const p = people.get(e.staff) ?? { minutes: 0, projects: new Map<string, number>() };
    p.minutes += e.minutes;
    p.projects.set(e.project, (p.projects.get(e.project) ?? 0) + e.minutes);
    people.set(e.staff, p);
  }
  const rows = [...people].sort((a, b) => b[1].minutes - a[1].minutes);
  const hhmm = (m: number) => `${Math.floor(m / 60)}:${String(m % 60).padStart(2, "0")}`;

  return <>
    <PageHeader title="Staff time overview" description="Recorded time per person. Cost rates live under Staff cost rates." />
    {error && <p role="alert" className="form-error">{error}</p>}
    {!selected && <p>Choose a company first.</p>}
    {selected && <>
      <div className="form-grid">
        <div><label htmlFor="staff-from">From</label><DateInput id="staff-from" value={from} onChange={(e) => setFrom(e.target.value)}/></div>
        <div><label htmlFor="staff-to">To</label><DateInput id="staff-to" value={to} min={from || undefined} onChange={(e) => setTo(e.target.value)}/></div>
      </div>
      <p className="section-help">Leave both dates blank to include every recorded entry (up to 500).</p>
      <div className="table-scroll"><table className="doc-table"><caption>Time by staff member</caption>
        <thead><tr><th>Staff</th><th>Total</th><th>Projects</th></tr></thead>
        <tbody>{rows.map(([name, v]) => <tr key={name}>
          <td>{name}</td><td>{hhmm(v.minutes)}</td>
          <td>{[...v.projects].map(([p, m]) => `${p} ${hhmm(m)}`).join(", ")}</td>
        </tr>)}</tbody>
      </table></div>
      {rows.length === 0 && <p>No time recorded for this period. <a href="/time/entries">Add a time entry</a>.</p>}
    </>}
  </>;
}
