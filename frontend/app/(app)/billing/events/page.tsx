"use client";
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type Event = { id: number; occurred_at: string; type: string; source: string };

export default function EventsPage() {
  const { selected } = useOrg();
  return <Events key={selected?.id ?? "none"} orgId={selected?.id} />;
}

function Events({ orgId }: { orgId?: number }) {
  const [rows, setRows] = useState<Event[]>([]);
  const [busy, setBusy] = useState(false);
  const [more, setMore] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    if (orgId == null) return;
    let live = true;
    api<{ events: Event[] }>(`/v1/events?org_id=${orgId}`).then(r => { if (live) { setRows(r.events); setMore(r.events.length === 100); } }).catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [orgId]);
  async function loadMore() {
    if (orgId == null || busy || rows.length === 0) return;
    setBusy(true); setError("");
    try {
      const result = await api<{ events: Event[] }>(`/v1/events?org_id=${orgId}&before_id=${rows[rows.length - 1].id}`);
      setRows(current => [...current, ...result.events]); setMore(result.events.length === 100);
    } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
    finally { setBusy(false); }
  }
  return <>
    <PageHeader title="Events" description="Company activity recorded by Tidal." />
    <p className="notice">This list shows recorded company activity, not events from external services.</p>
    {orgId == null && <p>Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}
    {orgId != null && <div className="table-scroll"><table className="doc-table"><caption>All events</caption><thead><tr><th>Occurred at</th><th>Event ID</th><th>Event type</th><th>Event source</th></tr></thead><tbody>{rows.map(row => <tr key={row.id}><td>{new Date(row.occurred_at).toLocaleString()}</td><td>{row.id}</td><td>{row.type}</td><td>{row.source}</td></tr>)}</tbody></table>{rows.length === 0 && !error && <p>No recorded events.</p>}</div>}
    {more && <button type="button" disabled={busy} onClick={() => void loadMore()}>{busy ? "Loading…" : "Load older events"}</button>}
  </>;
}
