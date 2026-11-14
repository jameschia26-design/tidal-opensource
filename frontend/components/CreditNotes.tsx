"use client";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { api, type ApiError } from "@/lib/api";
import { parseCents, formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Note = { id: number; credit_date: string; reason: string; amount_cents: number; journal_id: number };

// Credit notes reduce a posted invoice. They are append-only and cannot exceed the invoice total.
export default function CreditNotes({ orgId, invoiceId, balanceCents, canWrite, onChanged }: { orgId: number; invoiceId: number; balanceCents: number; canWrite: boolean; onChanged: () => Promise<void> }) {
  const live = useRef(true);
  const [notes, setNotes] = useState<Note[] | null>(null);
  const [date, setDate] = useState("");
  const [reason, setReason] = useState("");
  const [amount, setAmount] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  async function load() {
    const r = await api<{ credit_notes: Note[] }>(`/v1/invoices/${invoiceId}/credit-notes?org_id=${orgId}`);
    if (live.current) setNotes(r.credit_notes);
  }
  useEffect(() => {
    live.current = true;
    setNotes(null);
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // Keyed by company and invoice at the parent; reload when the invoice changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [invoiceId, orgId]);

  async function save(e: FormEvent) {
    e.preventDefault();
    const cents = parseCents(amount);
    if (!date || !reason.trim() || cents === null || cents < 1) {
      setError("Enter the date, reason and an amount above zero.");
      return;
    }
    if (busy) return;
    setBusy(true); setError(""); setNotice("");
    try {
      await api(`/v1/invoices/${invoiceId}/credit-notes`, { method: "POST", body: JSON.stringify({ org_id: orgId, credit_date: date, reason: reason.trim(), amount_cents: cents }) });
      if (!live.current) return;
      setDate(""); setReason(""); setAmount("");
      setNotice("Credit note recorded. The invoice itself was not changed.");
      await load();
      await onChanged();
    } catch (err) {
      if (live.current) { const x = err as ApiError; setError(messageFor(x.error, x.status)); }
    } finally {
      if (live.current) setBusy(false);
    }
  }

  return (
    <section>
      <h2>Credit notes</h2>
      <p className="section-help">Use a credit note to reduce what the customer owes for this invoice. It posts a reversing entry to Sales and Accounts Receivable.</p>
      {error && <p role="alert" className="form-error">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      {notes === null && !error && <p role="status">Loading…</p>}
      {notes && notes.length === 0 && <p className="muted">No credit notes.</p>}
      {notes && notes.length > 0 && (
        <div className="table-scroll"><table className="doc-table">
          <caption className="sr-only">Credit notes for this invoice</caption>
          <thead><tr><th scope="col">Date</th><th scope="col">Reason</th><th scope="col" className="num">Amount</th></tr></thead>
          <tbody>{notes.map((n) => <tr key={n.id}><td>{n.credit_date}</td><td className="long-text">{n.reason}</td><td className="num">{formatCents(n.amount_cents)}</td></tr>)}</tbody>
        </table></div>
      )}
      {canWrite && balanceCents > 0 && (
        <form className="record-form" onSubmit={save}>
          <fieldset disabled={busy}>
            <legend>New credit note</legend>
            <div className="form-grid">
              <div><label htmlFor="cn-date">Credit date</label><DateInput id="cn-date" required value={date} onChange={(e) => setDate(e.target.value)}/></div>
              <div><label htmlFor="cn-amount">Amount (SGD)</label><input id="cn-amount" required inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} /></div>
            </div>
            <label htmlFor="cn-reason">Reason</label>
            <input id="cn-reason" required maxLength={500} value={reason} onChange={(e) => setReason(e.target.value)} />
            <p className="section-help">Balance still owed: {formatCents(balanceCents)}. A credit note cannot take the total above what was invoiced.</p>
            <button disabled={busy}>{busy ? "Saving…" : "Record credit note"}</button>
          </fieldset>
        </form>
      )}
    </section>
  );
}
