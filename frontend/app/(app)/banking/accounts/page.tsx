"use client";
import Link from "next/link";
import { useEffect, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

// Bank accounts are ledger accounts flagged as bank. This screen owns the statement details; the
// chart of accounts keeps the code and the postings, so a balance never has two sources.
type Account = { id: number; code: string; name: string; account_number: string; account_type: string; opening_balance_cents: number; opening_balance_date: string; statement_balance_cents: number; imported_balance_cents: number; ledger_balance_cents: number; currency: string; archived: boolean; last_reconciled_to: string };
const TYPES = [["bank", "Bank"], ["credit_card", "Credit card"], ["paypal", "PayPal"], ["other", "Other"]] as const;
const blank = { name: "", code: "", account_number: "", account_type: "bank", opening: "", opening_date: "", statement: "", archived: false };

export default function BankAccounts() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}
function Workspace() {
  const { selected } = useOrg(); const orgId = selected?.id;
  const canWrite = selected != null && ["owner", "admin", "accountant"].includes(selected.role);
  const [accounts, setAccounts] = useState<Account[] | null>(null);
  const [form, setForm] = useState(blank); const [editing, setEditing] = useState<Account | null>(null);
  const [busy, setBusy] = useState(false); const [error, setError] = useState(""); const [notice, setNotice] = useState("");
  async function load(id: number) { const r = await api<{ accounts: Account[] }>(`/v1/bank/accounts?org_id=${id}`); setAccounts(r.accounts); }
  useEffect(() => { if (orgId != null) void load(orgId).catch((e: ApiError) => setError(messageFor(e.error, e.status))); }, [orgId]);
  function money(v: string): number | null { const t = v.trim(); if (!t) return 0; const n = Number(t); return Number.isFinite(n) ? Math.round(n * 100) : null; }
  function body(): string {
    return JSON.stringify({ org_id: orgId, name: form.name, code: form.code || undefined, account_number: form.account_number,
      account_type: form.account_type, opening_balance_cents: money(form.opening) ?? 0,
      opening_balance_date: form.opening_date || undefined, statement_balance_cents: money(form.statement) ?? 0,
      currency: "SGD", archived: form.archived });
  }
  async function save(e: FormEvent) {
    e.preventDefault(); if (!orgId || !canWrite || busy) return;
    setBusy(true); setError(""); setNotice("");
    try {
      if (editing) { await api(`/v1/bank/accounts/${editing.id}`, { method: "PUT", body: body() }); setNotice(`Bank account "${form.name}" updated.`); }
      else { await api("/v1/bank/accounts", { method: "POST", body: body() }); setNotice("Bank account added. Import a statement for it under Banking."); }
      setEditing(null); setForm(blank); await load(orgId);
    } catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } finally { setBusy(false); }
  }
  function edit(a: Account) {
    setEditing(a);
    setForm({ name: a.name, code: a.code, account_number: a.account_number, account_type: a.account_type,
      opening: (a.opening_balance_cents / 100).toFixed(2), opening_date: a.opening_balance_date,
      statement: (a.statement_balance_cents / 100).toFixed(2), archived: a.archived });
  }
  return <><PageHeader title="Bank accounts" description="Each bank account is a ledger account plus the statement details the bank shows." actions={<><Link href="/banking">Banking</Link> <Link href="/banking/rules">Bank rules</Link></>} />
    {!selected && <p>Choose a company first.</p>}{error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {canWrite && <form className="record-form" onSubmit={save}><fieldset disabled={busy}><legend>{editing ? `Edit ${editing.name}` : "Add bank account"}</legend>
      <div className="form-grid">
        <div><label htmlFor="ba-name">Account name</label><input id="ba-name" required maxLength={200} value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} /></div>
        <div><label htmlFor="ba-number">Account number</label><input id="ba-number" maxLength={50} value={form.account_number} onChange={e => setForm({ ...form, account_number: e.target.value })} /></div>
        <div><label htmlFor="ba-type">Account type</label><select id="ba-type" value={form.account_type} onChange={e => setForm({ ...form, account_type: e.target.value })}>{TYPES.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select></div>
        {!editing && <div><label htmlFor="ba-code">Ledger code (blank picks the next free)</label><input id="ba-code" maxLength={20} value={form.code} onChange={e => setForm({ ...form, code: e.target.value })} /></div>}
        <div><label htmlFor="ba-opening">Opening balance (SGD)</label><input id="ba-opening" inputMode="decimal" value={form.opening} onChange={e => setForm({ ...form, opening: e.target.value })} /></div>
        <div><label htmlFor="ba-opening-date">Opening balance date</label><DateInput id="ba-opening-date" value={form.opening_date} onChange={e => setForm({ ...form, opening_date: e.target.value })} /></div>
        <div><label htmlFor="ba-statement">Statement balance (SGD)</label><input id="ba-statement" inputMode="decimal" value={form.statement} onChange={e => setForm({ ...form, statement: e.target.value })} /><p className="section-help">Reconciling updates this to the statement closing balance.</p></div>
        <div><label className="check-label"><input type="checkbox" checked={form.archived} onChange={e => setForm({ ...form, archived: e.target.checked })} />Archived</label><p className="section-help">Currency is SGD only.</p></div>
      </div>
      <div className="form-actions"><button disabled={busy}>{editing ? "Save changes" : "Add bank account"}</button>{editing && <button type="button" className="button-secondary" onClick={() => { setEditing(null); setForm(blank); }}>Cancel</button>}</div>
    </fieldset></form>}
    {accounts === null && !error && <p role="status">Loading bank accounts…</p>}
    {accounts && accounts.length === 0 && <p>No bank accounts yet. Add one, then import its CSV statement.</p>}
    {accounts && accounts.length > 0 && <div className="table-scroll"><table className="doc-table">
      <caption>Bank accounts and their three balances</caption>
      <thead><tr><th>Name</th><th>Number</th><th>Type</th><th className="num">Opening</th><th className="num">Statement</th><th className="num">Balance in Tidal</th><th>Last reconciled</th><th>Actions</th></tr></thead>
      <tbody>{accounts.map(a => <tr key={a.id}>
        <th scope="row">{a.code} · {a.name}{a.archived ? " (archived)" : ""}</th><td>{a.account_number || "—"}</td><td>{TYPES.find(([v]) => v === a.account_type)?.[1] ?? a.account_type}</td>
        <td className="num">{formatCents(a.opening_balance_cents)}</td><td className="num">{formatCents(a.imported_balance_cents)}</td><td className="num">{formatCents(a.ledger_balance_cents)}</td>
        <td>{a.last_reconciled_to || "Never"}</td>
        <td><Link href={`/banking/reconcile?account=${a.id}`}>Reconcile {a.name}</Link>{canWrite && <> · <button className="button-secondary" disabled={busy} onClick={() => edit(a)}>Edit</button></>}</td>
      </tr>)}</tbody></table></div>}
  </>;
}
