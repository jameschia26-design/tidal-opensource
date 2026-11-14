"use client";
import { useEffect, useRef, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import { cashBankAccounts } from "@/components/ListSelects";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { parseCents, formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";
import { formatDate, todayISO } from "@/lib/dates";

type Profile = { id: number; name: string; repeat_every: number; repeat_unit: string; start_date: string; ends_on: string; next_run: string; payee: string; amount_cents: number; notes: string; active: boolean; generated_count: number; expense_account_id: number; customer_id: number };
type HistoryItem = { id: number; date: string; amount_cents: number };
type Account = { id: number; code: string; name: string; type: string };
type Contact = { id: number; name: string; role: string };
const today = todayISO;
const blank = () => ({ name: "", repeat_every: "1", repeat_unit: "month", start_date: today(), ends_on: "", never: true, expense_account_id: "", paid_through_account_id: "", customer_id: "", payee: "", amount: "", notes: "" });

export default function RecurringExpensesPage() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function Workspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const live = useRef(true);
  const [rows, setRows] = useState<Profile[] | null>(null);
  const [history, setHistory] = useState<{ name: string; expenses: HistoryItem[] } | null>(null);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [contacts, setContacts] = useState<Contact[]>([]);
  const [form, setForm] = useState(blank);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  async function load() {
    if (orgId == null) return;
    const [p, a, c] = await Promise.all([
      api<{ profiles: Profile[] }>(`/v1/recurring-expenses?org_id=${orgId}`),
      api<{ accounts: Account[] }>(`/v1/accounts?org_id=${orgId}`),
      api<{ contacts: Contact[] }>(`/v1/contacts?org_id=${orgId}&role=customer`),
    ]);
    if (!live.current) return;
    setRows(p.profiles); setAccounts(a.accounts); setContacts(c.contacts);
  }
  useEffect(() => {
    live.current = true;
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function call<T>(path: string, body: object, done: (r: T) => string) {
    if (busy || orgId == null) return;
    setBusy(true); setError(""); setNotice("");
    try {
      const r = await api<T>(path, { method: "POST", body: JSON.stringify({ org_id: orgId, ...body }) });
      if (!live.current) return;
      setNotice(done(r)); await load();
    } catch (e) { if (live.current) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } }
    finally { if (live.current) setBusy(false); }
  }
  async function showHistory(p: Profile) {
    if (busy || orgId == null) return;
    setBusy(true); setError("");
    try {
      const result = await api<{ expenses: HistoryItem[] }>(`/v1/recurring-expenses/${p.id}/history?org_id=${orgId}`);
      if (live.current) setHistory({ name: p.name, expenses: result.expenses });
    } catch (e) { if (live.current) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } }
    finally { if (live.current) setBusy(false); }
  }
  function save(e: FormEvent) {
    e.preventDefault();
    const cents = parseCents(form.amount), every = Number(form.repeat_every);
    if (!form.name.trim() || !form.payee.trim() || !form.expense_account_id || !form.paid_through_account_id || cents == null || cents <= 0 || !Number.isInteger(every) || every < 1 || every > 12 || !form.start_date || (!form.never && !form.ends_on)) {
      setError("Enter profile name, payee, accounts, a valid amount, repeat interval and dates."); return;
    }
    void call<{ next_run: string }>("/v1/recurring-expenses", { name: form.name.trim(), repeat_every: every, repeat_unit: form.repeat_unit, start_date: form.start_date, ends_on: form.never ? "" : form.ends_on, expense_account_id: Number(form.expense_account_id), paid_through_account_id: Number(form.paid_through_account_id), customer_id: Number(form.customer_id), payee: form.payee.trim(), amount_cents: cents, notes: form.notes }, r => { setOpen(false); setForm(blank()); return `Profile saved. First expense is due ${r.next_run}.`; });
  }
  const accountSelect = (id: string, value: string, type: string, change: (v: string) => void) => <select id={id} required value={value} onChange={e => change(e.target.value)}><option value="">Choose account</option>{(type === "asset" ? cashBankAccounts(accounts) : accounts.filter(a => a.type === type)).map(a => <option key={a.id} value={a.id}>{a.code} — {a.name}</option>)}</select>;
  const every = Number(form.repeat_every);

  return <>
    <PageHeader title="Recurring expenses" description="Profiles that post the same paid expense on a schedule." />
    <p className="notice">SGD, no GST. Nothing posts by itself: press Run due expenses to post every expense that is due today or earlier. Each due date posts once. Stopping a profile keeps expenses already posted.</p>
    {!selected && <p>Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {selected && canWrite && <div className="form-actions"><button disabled={busy || open} onClick={() => { setOpen(true); setForm(blank()); }}>New recurring expense</button><button className="button-secondary" disabled={busy} onClick={() => void call<{ created_expense_ids: number[] }>("/v1/recurring-expenses/run", { as_of: today() }, r => `${r.created_expense_ids.length} expense(s) posted.`)}>Run due expenses</button></div>}
    {open && canWrite && <form className="record-form" onSubmit={save}>
      <fieldset disabled={busy}><legend>Profile</legend><div className="form-grid">
        <div><label htmlFor="rx-name">Profile name</label><input id="rx-name" required maxLength={200} value={form.name} onChange={e => setForm({ ...form, name: e.target.value })} /></div>
        <div><label htmlFor="rx-every">Repeat every</label><input id="rx-every" required inputMode="numeric" value={form.repeat_every} onChange={e => setForm({ ...form, repeat_every: e.target.value })} /></div>
        <div><label htmlFor="rx-unit">Unit</label><select id="rx-unit" value={form.repeat_unit} onChange={e => setForm({ ...form, repeat_unit: e.target.value })}><option value="week">Week(s)</option><option value="month">Month(s)</option><option value="year">Year(s)</option></select></div>
        <div><label htmlFor="rx-start">Start date</label><DateInput id="rx-start" required value={form.start_date} onChange={e => setForm({ ...form, start_date: e.target.value })}/><p className="section-help">First expense posts on this date{Number.isInteger(every) && every > 0 ? `, then every ${every} ${form.repeat_unit}${every > 1 ? "s" : ""}` : ""}.</p></div>
        <div><label className="check-label"><input type="checkbox" checked={form.never} onChange={e => setForm({ ...form, never: e.target.checked })} />Never expires</label>{!form.never && <><label htmlFor="rx-ends">Ends on</label><DateInput id="rx-ends" required min={form.start_date} value={form.ends_on} onChange={e => setForm({ ...form, ends_on: e.target.value })}/></>}</div>
      </div></fieldset>
      <fieldset disabled={busy}><legend>Expense</legend><div className="form-grid">
        <div><label htmlFor="rx-payee">Paid to</label><input id="rx-payee" required maxLength={200} value={form.payee} onChange={e => setForm({ ...form, payee: e.target.value })} /></div>
        <div><label htmlFor="rx-account">Category</label>{accountSelect("rx-account", form.expense_account_id, "expense", v => setForm({ ...form, expense_account_id: v }))}</div>
        <div><label htmlFor="rx-paid">Paid through</label>{accountSelect("rx-paid", form.paid_through_account_id, "asset", v => setForm({ ...form, paid_through_account_id: v }))}</div>
        <div><label htmlFor="rx-amount">Amount (SGD)</label><input id="rx-amount" required inputMode="decimal" value={form.amount} onChange={e => setForm({ ...form, amount: e.target.value })} /></div>
        <div><label htmlFor="rx-customer">Customer (optional)</label><select id="rx-customer" value={form.customer_id} onChange={e => setForm({ ...form, customer_id: e.target.value })}><option value="">No customer</option>{contacts.filter(c => c.role !== "supplier").map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select></div>
        <div><label htmlFor="rx-notes">Notes (optional)</label><textarea id="rx-notes" rows={3} maxLength={4000} value={form.notes} onChange={e => setForm({ ...form, notes: e.target.value })} /><p className="section-help">Copied to every generated expense. Reporting tags are not available in Tidal.</p></div>
      </div></fieldset>
      <div className="form-actions"><button disabled={busy}>{busy ? "Saving…" : "Save profile"}</button><button type="button" className="button-secondary" disabled={busy} onClick={() => setOpen(false)}>Cancel</button></div>
    </form>}
    {rows === null && !error && <p role="status">Loading profiles…</p>}{rows?.length === 0 && <p>No recurring expense profiles yet.</p>}
    {!!rows?.length && <div className="table-scroll"><table className="doc-table"><caption>Recurring expense profiles</caption><thead><tr><th>Profile</th><th>Paid to</th><th>Repeats</th><th>Next due</th><th>Ends</th><th>Posted</th><th>Amount</th><th>Status</th><th>Action</th></tr></thead><tbody>{rows.map(p => <tr key={p.id}><td>{p.name}</td><td>{p.payee}</td><td>Every {p.repeat_every} {p.repeat_unit}{p.repeat_every > 1 ? "s" : ""}</td><td>{p.active ? p.next_run : "—"}</td><td>{p.ends_on || "Never"}</td><td><button type="button" className="button-secondary" disabled={busy} onClick={() => void showHistory(p)}>{p.generated_count} — View history<span className="sr-only"> for {p.name}</span></button></td><td>{formatCents(p.amount_cents)}</td><td>{p.active ? "Active" : "Stopped"}</td><td>{canWrite && <button className="button-secondary" disabled={busy} onClick={() => void call<{ active: boolean }>("/v1/recurring-expenses/active", { id: p.id, active: !p.active }, r => r.active ? "Profile resumed." : "Profile stopped.")}>{p.active ? "Stop" : "Resume"}<span className="sr-only"> {p.name}</span></button>}</td></tr>)}</tbody></table></div>}
    {history && <section aria-labelledby="history-title"><h2 id="history-title">Posted expenses — {history.name}</h2><p>Showing up to the latest 200 expenses.</p><button type="button" className="button-secondary" onClick={() => setHistory(null)}>Close history</button>{history.expenses.length === 0 ? <p>No expenses posted yet.</p> : <div className="table-scroll"><table className="doc-table"><caption>Posted expenses for {history.name}</caption><thead><tr><th>Date</th><th>Expense ID</th><th>Amount</th></tr></thead><tbody>{history.expenses.map(e => <tr key={e.id}><td>{formatDate(e.date)}</td><td>{e.id}</td><td>{formatCents(e.amount_cents)}</td></tr>)}</tbody></table></div>}</section>}
  </>;
}
