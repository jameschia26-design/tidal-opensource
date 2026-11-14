"use client";
import { useEffect, useRef, useState } from "react";
import PageHeader from "@/components/PageHeader";
import PreviewNotice from "@/components/PreviewNotice";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

// Period close is one-way. Once closed, no entry dated on or before the date can be recorded anywhere in Tidal.
export default function PeriodClosePage() {
  const { selected } = useOrg();
  return <PeriodCloseWorkspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function PeriodCloseWorkspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canClose = selected != null && ["owner", "admin", "accountant"].includes(selected.role);
  const live = useRef(true);
  const [closedThrough, setClosedThrough] = useState<string | null | undefined>(undefined);
  const [date, setDate] = useState("");
  const [confirm, setConfirm] = useState(false);
  const [preview, setPreview] = useState<{ journals: number; stock_movements: number; drafts_to_review: number } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  async function load() {
    if (orgId == null) return;
    const r = await api<{ closed_through: string | null }>(`/v1/period-close?org_id=${orgId}`);
    if (live.current) setClosedThrough(r.closed_through);
  }
  useEffect(() => {
    live.current = true;
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Reviewing fetches what the close would lock, so the confirmation names real counts.
  async function review() {
    if (!date || busy || orgId == null) return;
    setBusy(true); setError("");
    try {
      const r = await api<{ preview?: { journals: number; stock_movements: number; drafts_to_review: number } }>(`/v1/period-close?org_id=${orgId}&through=${date}`);
      if (!live.current) return;
      setPreview(r.preview ?? null);
      setConfirm(true);
    } catch (err) {
      if (live.current) { const x = err as ApiError; setError(messageFor(x.error, x.status)); }
    } finally {
      if (live.current) setBusy(false);
    }
  }

  async function close() {
    if (!date || busy || orgId == null) return;
    setBusy(true); setError(""); setNotice("");
    try {
      const r = await api<{ closed_through: string }>("/v1/period-close", { method: "POST", body: JSON.stringify({ org_id: orgId, closed_through: date }) });
      if (!live.current) return;
      setClosedThrough(r.closed_through);
      setConfirm(false); setDate("");
      setNotice(`Books closed through ${r.closed_through}.`);
    } catch (err) {
      if (live.current) { const x = err as ApiError; setError(messageFor(x.error, x.status)); }
    } finally {
      if (live.current) setBusy(false);
    }
  }

  return (
    <>
      <PageHeader title="Period close" description="Lock finished months so older entries cannot change." />
      <PreviewNotice>Closing cannot be undone in Tidal and the date can only move forward. Check your reports before closing.</PreviewNotice>
      {!selected && <p>Choose a company first.</p>}
      {error && <p role="alert" className="form-error">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      {selected && closedThrough === undefined && !error && <p role="status">Loading…</p>}
      {selected && closedThrough !== undefined && (
        <p>Currently closed through: <strong>{closedThrough ?? "nothing closed yet"}</strong></p>
      )}
      {selected && canClose && (
        <form className="record-form" onSubmit={(e) => { e.preventDefault(); void review(); }}>
          <h2>Close books</h2>
          <fieldset disabled={busy}>
            <label htmlFor="pc-date">Close through (inclusive)</label>
            <DateInput id="pc-date" required value={date} onChange={(e) => { setDate(e.target.value); setConfirm(false); }}/>
            <button disabled={busy || !date}>Review close</button>
          </fieldset>
        </form>
      )}
      {selected && !canClose && <p className="muted">Only owners, admins and accountants can close the books.</p>}
      {confirm && (
        <div role="group" aria-label="Confirm period close">
          <p>Close all entries dated on or before <strong>{date}</strong>? This cannot be undone here.</p>
          {preview && (
            <p>
              This locks {preview.journals} journal{preview.journals === 1 ? "" : "s"} and {preview.stock_movements} stock movement{preview.stock_movements === 1 ? "" : "s"}.
              {preview.drafts_to_review > 0 && ` ${preview.drafts_to_review} draft document${preview.drafts_to_review === 1 ? "" : "s"} fall in this period and cannot be posted afterwards.`}
            </p>
          )}
          <button disabled={busy} onClick={close}>Confirm close</button>
          <button className="button-secondary" disabled={busy} onClick={() => setConfirm(false)}>Cancel</button>
        </div>
      )}
    </>
  );
}
