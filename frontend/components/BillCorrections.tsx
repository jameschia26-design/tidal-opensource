"use client";
import { useEffect, useState } from "react";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { parseCents, formatCents, formatMoneyInput } from "@/lib/money";
import DateInput from "@/components/DateInput";

type Account = { id: number; code: string; name: string; type: string };
const today = () => new Date().toISOString().slice(0, 10);

/** Corrections on a posted bill: a supplier credit, its refund, and a full void. Nothing edits history. */
export default function BillCorrections({ orgId, billId, canWrite, onChanged }: {
  orgId: number; billId: number; canWrite: boolean; onChanged: (message: string) => void;
}) {
  const [reverseDate, setReverseDate] = useState(today());
  const [creditDate, setCreditDate] = useState(today());
  const [reason, setReason] = useState("");
  const [net, setNet] = useState("");
  const [gst, setGst] = useState("");
  const [credit, setCredit] = useState<{ id: number; total_cents: number } | null>(null);
  const [refundDate, setRefundDate] = useState(today());
  const [bank, setBank] = useState("");
  const [banks, setBanks] = useState<Account[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    let live = true;
    api<{ accounts: Account[] }>(`/v1/accounts?org_id=${orgId}`)
      .then((r) => { if (!live) return; const list = r.accounts.filter((a) => a.type === "asset"); setBanks(list); setBank((b) => b || (list[0] ? String(list[0].id) : "")); })
      .catch(() => { if (live) setError("Could not load accounts."); });
    return () => { live = false; };
  }, [orgId]);

  async function send(path: string, body: unknown, done: string) {
    setBusy(true); setError("");
    try {
      await api(path, { method: "POST", body: JSON.stringify({ org_id: orgId, ...(body as object) }) });
      onChanged(done);
    } catch (err) {
      const x = err as ApiError;
      setError(messageFor(x.error, x.status));
    } finally { setBusy(false); }
  }

  async function makeCredit(e: React.FormEvent) {
    e.preventDefault();
    const n = parseCents(net), g = parseCents(gst || "0");
    if (n == null || g == null || n + g <= 0) { setError("Enter the credit net and GST amounts."); return; }
    setBusy(true); setError("");
    try {
      const made = await api<{ id: number; total_cents: number }>(`/v1/bills/${billId}/credit-notes`, {
        method: "POST",
        body: JSON.stringify({ org_id: orgId, credit_date: creditDate, reason: reason.trim(), net_cents: n, gst_cents: g }),
      });
      setCredit(made); setNet(""); setGst(""); setReason("");
      onChanged(`Supplier credit recorded for ${formatCents(made.total_cents)}.`);
    } catch (err) {
      const x = err as ApiError;
      setError(messageFor(x.error, x.status));
    } finally { setBusy(false); }
  }

  if (!canWrite) return null;
  return (
    <section aria-label="Corrections">
      <h2>Credit note or void</h2>
      {error && <p role="alert" className="form-error">{error}</p>}
      <form className="record-form" onSubmit={makeCredit}>
        <fieldset disabled={busy}>
          <legend>Supplier credit</legend>
          <p className="muted small">Reduces what you owe on this bill. It posts its own journal; the bill itself is unchanged.</p>
          <div className="form-grid">
            <div><label htmlFor="bc-credit-date">Credit date</label><DateInput id="bc-credit-date" required value={creditDate} onChange={(e) => setCreditDate(e.target.value)} /></div>
            <div><label htmlFor="bc-net">Net (SGD)</label><input id="bc-net" required inputMode="decimal" value={net} onChange={(e) => setNet(formatMoneyInput(e.target.value))} /></div>
            <div><label htmlFor="bc-gst">GST (SGD)</label><input id="bc-gst" inputMode="decimal" value={gst} onChange={(e) => setGst(formatMoneyInput(e.target.value))} /></div>
            <div><label htmlFor="bc-reason">Reason <span className="field-optional">Optional</span></label><input id="bc-reason" maxLength={200} value={reason} onChange={(e) => setReason(e.target.value)} /></div>
          </div>
          <div className="form-actions"><button type="submit" disabled={busy}>{busy ? "Saving…" : "Record credit"}</button></div>
        </fieldset>
      </form>
      {credit && (
        <form className="record-form" onSubmit={(e) => { e.preventDefault(); if (bank) void send(`/v1/supplier-credits/${credit.id}/refund`, { refund_date: refundDate, bank_account_id: Number(bank) }, `Refund of ${formatCents(credit.total_cents)} recorded.`); }}>
          <fieldset disabled={busy}>
            <legend>Refund credit {formatCents(credit.total_cents)}</legend>
            <div className="form-grid">
              <div><label htmlFor="bc-refund-date">Refund date</label><DateInput id="bc-refund-date" required value={refundDate} onChange={(e) => setRefundDate(e.target.value)} /></div>
              <div><label htmlFor="bc-bank">Paid from</label><select id="bc-bank" required value={bank} onChange={(e) => setBank(e.target.value)}>{banks.map((a) => <option key={a.id} value={a.id}>{a.code} {a.name}</option>)}</select></div>
            </div>
            <div className="form-actions"><button type="submit" disabled={busy || !bank}>{busy ? "Saving…" : "Record refund"}</button></div>
          </fieldset>
        </form>
      )}
      <form className="record-form" onSubmit={(e) => { e.preventDefault(); void send(`/v1/bills/${billId}/void`, { reversal_date: reverseDate }, "Bill voided. A reversing journal was posted and the bill stays in the history."); }}>
        <fieldset disabled={busy}>
          <legend>Void the whole bill</legend>
          <p className="muted small">Posts the opposite journal on the date you choose and marks the bill voided, so it stops affecting the books. It cannot be undone. Use a credit note for a partial correction. A bill with a payment or credit must have those reversed first.</p>
          <div className="form-grid"><div><label htmlFor="bc-reverse-date">Void date</label><DateInput id="bc-reverse-date" required value={reverseDate} onChange={(e) => setReverseDate(e.target.value)} /></div></div>
          <div className="form-actions"><button type="submit" className="button-secondary" disabled={busy}>{busy ? "Voiding…" : "Void bill"}</button></div>
        </fieldset>
      </form>
    </section>
  );
}
