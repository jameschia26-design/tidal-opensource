"use client";
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type Event = {
  id: number;
  created_at: string;
  user_id: number | null;
  user_email: string;
  action: string;
  entity_kind: string | null;
  entity_id: number | null;
  detail: unknown;
};

const KINDS = ["", "invoice", "payment", "credit_note", "bill", "bill_payment", "expense", "period_close", "journal"];

export default function AuditHistory() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canRead = selected != null && (selected.role === "owner" || selected.role === "admin");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [kind, setKind] = useState("");
  const [entityId, setEntityId] = useState("");
  const [userId, setUserId] = useState("");
  const [events, setEvents] = useState<Event[] | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  function query(extra = "") {
    const p = new URLSearchParams({ org_id: String(orgId) });
    if (from) p.set("from", from);
    if (to) p.set("to", to);
    if (kind) p.set("entity_kind", kind);
    if (entityId) p.set("entity_id", entityId);
    if (userId) p.set("user_id", userId);
    p.set("limit", "200");
    return `/v1/audit-events?${p.toString()}${extra}`;
  }

  async function load() {
    if (orgId == null) return;
    setBusy(true); setError("");
    try {
      const body = await api<{ events: Event[] }>(query());
      setEvents(body.events);
    } catch (err) {
      setEvents(null);
      const e = err as ApiError;
      setError(messageFor(e.error, e.status));
    } finally {
      setBusy(false);
    }
  }

  useEffect(() => {
    setEvents(null); setError("");
    if (canRead) void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [orgId, canRead]);

  async function exportCsv() {
    if (orgId == null) return;
    setError("");
    const res = await fetch(`/api${query("&format=csv")}`, { credentials: "same-origin", cache: "no-store" });
    if (!res.ok) {
      setError("Could not export the history. Only owners and admins can read it.");
      return;
    }
    const blob = await res.blob();
    const url = URL.createObjectURL(blob);
    const link = document.createElement("a");
    link.href = url;
    link.download = "audit-history.csv";
    link.click();
    URL.revokeObjectURL(url);
  }

  return (
    <>
      <PageHeader title="Audit history" description="Every change to the books, with who made it and which record it touched." />
      {!canRead && orgId != null && <p className="notice">Only owners and admins can read the audit history.</p>}
      {canRead && (
        <>
          <form className="form-grid" onSubmit={(e) => { e.preventDefault(); void load(); }}>
            <div><label htmlFor="ah-from">From</label><input id="ah-from" type="date" value={from} onChange={(e) => setFrom(e.target.value)} /></div>
            <div><label htmlFor="ah-to">To</label><input id="ah-to" type="date" value={to} onChange={(e) => setTo(e.target.value)} /></div>
            <div>
              <label htmlFor="ah-kind">Record type</label>
              <select id="ah-kind" value={kind} onChange={(e) => setKind(e.target.value)}>
                {KINDS.map((k) => <option key={k} value={k}>{k === "" ? "Any record" : k.replace(/_/g, " ")}</option>)}
              </select>
            </div>
            <div><label htmlFor="ah-entity">Record number</label><input id="ah-entity" inputMode="numeric" value={entityId} onChange={(e) => setEntityId(e.target.value)} /></div>
            <div><label htmlFor="ah-user">User number</label><input id="ah-user" inputMode="numeric" value={userId} onChange={(e) => setUserId(e.target.value)} /></div>
            <div className="form-action"><button type="submit" disabled={busy}>{busy ? "Loading…" : "Filter"}</button></div>
          </form>
          <p><button type="button" className="button-secondary" onClick={() => void exportCsv()}>Export CSV</button></p>
        </>
      )}
      {error && <p role="alert" className="form-error">{error}</p>}
      {events == null && canRead && <p role="status">Loading history…</p>}
      {events && events.length === 0 && <p>No events match those filters.</p>}
      {events && events.length > 0 && (
        <div className="table-scroll">
          <table className="doc-table">
            <caption>Audit history, newest first</caption>
            <thead><tr><th>When</th><th>Who</th><th>Action</th><th>Record</th><th>Details</th></tr></thead>
            <tbody>
              {events.map((e) => (
                <tr key={e.id}>
                  <td>{e.created_at}</td>
                  <td>{e.user_email || "system"}</td>
                  <td>{e.action}</td>
                  <td>{e.entity_kind ? `${e.entity_kind} ${e.entity_id ?? ""}` : "—"}</td>
                  <td className="long-text">{JSON.stringify(e.detail)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}
