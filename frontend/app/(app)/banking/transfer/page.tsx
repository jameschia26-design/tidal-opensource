"use client";
import { useEffect, useRef, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { formatCents, parseCents } from "@/lib/money";
import DateInput from "@/components/DateInput";
import { formatDate, todayISO } from "@/lib/dates";

type Account = { id: number; code: string; name: string; type: string };
type Transfer = { id: number; entry_date: string; memo: string; amount_cents: number; from_account_id: number; to_account_id: number };
const today = todayISO;

export default function TransferMoney() {
  const { selected } = useOrg();
  return <TransferWorkspace key={selected?.id ?? "none"} />;
}

function TransferWorkspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const active = useRef(orgId);
  useEffect(() => { active.current = orgId; }, [orgId]);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [transfers, setTransfers] = useState<Transfer[]>([]);
  const [from, setFrom] = useState(""); const [to, setTo] = useState("");
  const [date, setDate] = useState(today); const [amount, setAmount] = useState(""); const [reference, setReference] = useState("");
  const [busy, setBusy] = useState(false); const [error, setError] = useState(""); const [notice, setNotice] = useState("");
  const [loaded, setLoaded] = useState(false);
  const key = useRef("");
  const canWrite = selected && ["owner", "admin", "accountant"].includes(selected.role);

  async function reload(id: number) {
    const [a, t] = await Promise.all([api<{ accounts: Account[] }>(`/v1/accounts?org_id=${id}`), api<{ transfers: Transfer[] }>(`/v1/transfers?org_id=${id}`)]);
    if (active.current === id) { setAccounts(a.accounts.filter((x) => x.type === "asset")); setTransfers(t.transfers); setLoaded(true); }
  }
  useEffect(() => {
    setLoaded(false); setError(""); setNotice(""); setFrom(""); setTo(""); setAmount(""); setReference(""); setDate(today()); key.current = "";
    if (orgId == null) return;
    let live = true;
    reload(orgId).catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [orgId]);

  const cents = amount === "" ? null : parseCents(amount);
  const valid = cents !== null && cents > 0 && from !== "" && to !== "" && from !== to;

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (!valid || busy || orgId == null) return;
    const id = orgId; setBusy(true); setError(""); setNotice("");
    key.current ||= crypto.randomUUID();
    try {
      await api("/v1/transfers", { method: "POST", body: JSON.stringify({ org_id: id, from_account_id: Number(from), to_account_id: Number(to), entry_date: date, amount_cents: cents, reference, idempotency_key: key.current }) });
      if (active.current !== orgId) return;
      key.current = ""; setAmount(""); setReference(""); setNotice("Transfer saved."); await reload(id);
    } catch (err) {
      const e2 = err as ApiError;
      if (active.current === id) setError(messageFor(e2.error, e2.status));
    } finally { setBusy(false); }
  }

  const name = (id: number) => { const a = accounts.find((x) => x.id === id); return a ? `${a.code} · ${a.name}` : String(id); };
  return <>
    <PageHeader title="Transfer money" description="Move an amount between two of your accounts. Both sides post to the ledger at the same time." />
    {!selected && <p className="notice">Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}
    {notice && <p role="status">{notice}</p>}
    {selected && !loaded && !error && <p role="status">Loading accounts…</p>}
    {selected && loaded && canWrite && <form className="record-form" onSubmit={submit}>
      <fieldset disabled={busy}>
        <legend>Transfer details</legend>
        <div className="form-grid">
          <div><label htmlFor="transfer-from">From account</label><select id="transfer-from" required value={from} onChange={(e) => { setFrom(e.target.value); key.current = ""; }}><option value="">Choose account</option>{accounts.map((a) => <option key={a.id} value={a.id}>{a.code} · {a.name}</option>)}</select></div>
          <div><label htmlFor="transfer-to">To account</label><select id="transfer-to" required value={to} onChange={(e) => { setTo(e.target.value); key.current = ""; }}><option value="">Choose account</option>{accounts.map((a) => <option key={a.id} value={a.id} disabled={String(a.id) === from}>{a.code} · {a.name}</option>)}</select></div>
          <div><label htmlFor="transfer-date">Date</label><DateInput id="transfer-date" required value={date} onChange={(e) => { setDate(e.target.value); key.current = ""; }}/></div>
          <div><label htmlFor="transfer-amount">Amount (SGD)</label><input id="transfer-amount" required inputMode="decimal" pattern="[0-9]+(\.[0-9]{1,2})?" value={amount} onChange={(e) => { setAmount(e.target.value); key.current = ""; }} /></div>
          <div><label htmlFor="transfer-reference">Reference <span className="field-optional">Optional</span></label><input id="transfer-reference" maxLength={200} value={reference} onChange={(e) => { setReference(e.target.value); key.current = ""; }} /></div>
        </div>
        {from !== "" && from === to && <p role="alert" className="form-error">Choose two different accounts.</p>}
      </fieldset>
      <button disabled={busy || !valid} type="submit">{busy ? "Saving…" : "Transfer"}</button><button type="button" className="button-secondary" onClick={() => { setFrom(""); setTo(""); setAmount(""); setReference(""); setDate(today()); key.current = ""; }}>Cancel</button>
    </form>}
    {selected && loaded && !canWrite && <p className="notice">Your role allows viewing only.</p>}
    {selected && loaded && accounts.length < 2 && <p className="notice">Add a second asset account (for example a bank or cash account) in the chart of accounts before transferring.</p>}
    {selected && loaded && (transfers.length > 0
      ? <div className="table-scroll"><table className="doc-table"><caption>Recent transfers</caption><thead><tr><th>Date</th><th>From</th><th>To</th><th className="num">Amount</th><th>Journal</th></tr></thead><tbody>{transfers.map((t) => <tr key={t.id}><td>{formatDate(t.entry_date)}</td><td>{name(t.from_account_id)}</td><td>{name(t.to_account_id)}</td><td className="num">{formatCents(t.amount_cents)}</td><td>{t.id}</td></tr>)}</tbody></table></div>
      : <p className="notice">No transfers yet.</p>)}
  </>;
}
