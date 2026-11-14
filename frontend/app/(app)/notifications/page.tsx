"use client";
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api } from "@/lib/api";

type Note = { id: number; action: string; actor: string; created_at: string };
const seenKey = (orgId: number) => `tidal:notifications-seen:${orgId}`;

export default function NotificationsPage() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const [rows, setRows] = useState<Note[]>([]);
  const [seen, setSeen] = useState(0);
  useEffect(() => {
    if (!orgId) return;
    let live = true;
    api<{ notifications: Note[] }>(`/v1/notifications?org_id=${orgId}`)
      .then(r => { if (live) setRows(r.notifications); })
      .catch(() => { if (live) setRows([]); });
    setSeen(Number(localStorage.getItem(seenKey(orgId)) ?? 0));
    return () => { live = false; };
  }, [orgId]);
  function markRead() {
    if (!orgId) return;
    const highest = rows.reduce((n, r) => Math.max(n, r.id), 0);
    localStorage.setItem(seenKey(orgId), String(highest));
    setSeen(highest);
  }
  const unread = rows.filter(r => r.id > seen).length;
  return <>
    <PageHeader title="Notifications" description="Recent activity in this company, newest first." actions={rows.length > 0 ? <button type="button" className="button-secondary" onClick={markRead}>Mark all read on this device</button> : undefined} />
    {!orgId && <p>Choose a company first.</p>}
    {orgId && <p role="status">{unread} unread of {rows.length} recent events.</p>}
    {rows.length === 0 ? <p className="muted">No activity recorded yet.</p> : <div className="table-scroll"><table className="doc-table"><caption>Recent activity</caption><thead><tr><th>When</th><th>What happened</th><th>Who</th><th>Read</th></tr></thead><tbody>
      {rows.map(r => <tr key={r.id}><td>{new Date(r.created_at).toLocaleString()}</td><th scope="row">{r.action.replace(/[._]/g, " ")}</th><td>{r.actor || "System"}</td><td>{r.id > seen ? "Unread" : "Read"}</td></tr>)}
    </tbody></table></div>}
  </>;
}
