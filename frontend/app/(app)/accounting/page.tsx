"use client";
import { useEffect, useRef, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { formatCents, parseCents } from "@/lib/money";
import DateInput from "@/components/DateInput";
import { todayISO } from "@/lib/dates";
import { CLASS_TABS, classInTab, classLabel, classesFor, defaultAccountClass } from "@/lib/account-classes";

type Account = { id: number; code: string; name: string; type: string; class: string; description: string; show_on_dashboard: boolean; show_in_expense_claims: boolean; enable_payments: boolean; tax_rate_code: string; ytd_cents: number; is_bank_account?: boolean };
// Tax codes a document line can post with today. Imports, reverse charge and bad debt stay in the
// catalogue until their self-assessed posting exists.
const TAX_CODES = [["", "No default"], ["SR", "GST 9%"], ["ZR", "Zero-rated"], ["ES", "Exempt"], ["OS", "Out of scope"], ["NONE", "No tax"]] as const;
type Journal = { id: number; entry_date: string; memo: string; total_cents: number; reversal_of: number; reversed_by: number; manual: boolean; status: string };
type Line = { description?: string; account_id: number; code: string; name: string; debit_cents: number; credit_cents: number };
const today = todayISO;
const blankLine = () => ({ description: "", account_id: "", debit: "", credit: "" });

export default function Accounting() {
  const { selected } = useOrg();
  return <AccountingWorkspace key={selected?.id ?? "none"} />;
}

function AccountingWorkspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const active = useRef(orgId);
  useEffect(() => { active.current = orgId; }, [orgId]);
  const [tab, setTab] = useState("accounts");
  useEffect(() => {
    const syncTab = () => setTab(new URLSearchParams(window.location.search).get("tab") === "journals" ? "journals" : "accounts");
    syncTab();
    window.addEventListener("popstate", syncTab);
  return () => window.removeEventListener("popstate", syncTab);
  }, []);
  function selectTab(next: string) {
    setTab(next);
    window.history.pushState(null, "", `?tab=${next}`);
  }
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [journals, setJournals] = useState<Journal[]>([]);
  const [journalFilter, setJournalFilter] = useState("all");
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [bulkIDs, setBulkIDs] = useState<number[]>([]);
  const [bulkTax, setBulkTax] = useState("");
  const [account, setAccount] = useState({ code: "", name: "", type: "expense", class: "expense", description: "", show_on_dashboard: false, show_in_expense_claims: false, enable_payments: false, is_bank_account: false, tax_rate_code: "" });
  const [classTab, setClassTab] = useState("all");
  const [date, setDate] = useState(today);
  const [reverseDate, setReverseDate] = useState("");
  const [memo, setMemo] = useState("");
  const [copyNarration, setCopyNarration] = useState(false);
  const [lines, setLines] = useState([blankLine(), blankLine()]);
  const key = useRef("");
  const [detail, setDetail] = useState<{ id: number; lines: Line[] } | null>(null);
  const [confirm, setConfirm] = useState<Journal | null>(null);
  const [confirmDate, setConfirmDate] = useState(today);
  const [importText, setImportText] = useState("");
  const canWrite = selected && ["owner", "admin", "accountant"].includes(selected.role);

  async function bulkTaxRate(e: React.FormEvent) {
    e.preventDefault();
    if (orgId == null || !bulkIDs.length || !canWrite) return;
    setBusy(true); setError(""); setNotice("");
    try {
      const r = await api<{ updated: number }>("/v1/accounts/bulk-tax-rate", { method: "POST", body: JSON.stringify({ org_id: orgId, account_ids: bulkIDs, tax_rate_code: bulkTax }) });
      setNotice(`Default tax rate updated on ${r.updated} account(s).`);
      setBulkIDs([]);
      await reload(orgId);
    } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
    finally { setBusy(false); }
  }


  async function reload(id: number) {
    const [a, j] = await Promise.all([api<{ accounts: Account[] }>(`/v1/accounts?org_id=${id}`), api<{ journals: Journal[] }>(`/v1/journals?org_id=${id}`)]);
    if (active.current === id) { setAccounts(a.accounts); setJournals(j.journals); setLoaded(true); }
  }
  useEffect(() => {
    setLoaded(false); setError(""); setNotice(""); setDetail(null); setConfirm(null); setLines([blankLine(), blankLine()]); setAccount({ code: "", name: "", type: "expense", class: "expense", description: "", show_on_dashboard: false, show_in_expense_claims: false, enable_payments: false, is_bank_account: false, tax_rate_code: "" }); setClassTab("all"); setMemo(""); setDate(today()); key.current = "";
    if (orgId == null) return;
    let live = true;
    reload(orgId).catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
  return () => { live = false; };
  }, [orgId]);

  async function action(work: () => Promise<void>) {
    if (busy || orgId == null) return;
    const id = orgId; setBusy(true); setError(""); setNotice("");
    try { await work(); } catch (e) { if (active.current === id) { const err = e as ApiError; setError(messageFor(err.error, err.status)); } }
    finally { setBusy(false); }
  }
  async function importJournals(e: FormEvent) {
    e.preventDefault();
    if (orgId == null || !importText.trim()) return;
    const byKey = new Map<string, { date: string; memo: string; lines: { description: string; account_id: number; debit_cents: number; credit_cents: number }[] }>();
    const codeToID = new Map(accounts.map(a => [a.code, a.id]));
    for (const [index, raw] of importText.split(/\r?\n/).entries()) {
      const line = raw.trim();
      if (!line) continue;
      const [date, memo, code, debit, credit] = line.split(",").map(v => v.trim());
      const accountID = codeToID.get(code);
      if (!/^\d{4}-\d{2}-\d{2}$/.test(date) || !memo || !accountID) { setError(`Row ${index + 1}: use date,narration,account code,debit,credit with an existing account code.`); return; }
      const key = `${date}|${memo}`;
      const group = byKey.get(key) ?? { date, memo, lines: [] };
      group.lines.push({ description: memo, account_id: accountID, debit_cents: debit ? Math.round(Number(debit) * 100) : 0, credit_cents: credit ? Math.round(Number(credit) * 100) : 0 });
      byKey.set(key, group);
    }
    for (const group of byKey.values()) {
      const dr = group.lines.reduce((n, l) => n + l.debit_cents, 0), cr = group.lines.reduce((n, l) => n + l.credit_cents, 0);
      if (dr === 0 || dr !== cr) { setError(`Journal "${group.memo}" on ${group.date} does not balance.`); return; }
    }
    await action(async () => {
      for (const group of byKey.values()) await api("/v1/journals", { method: "POST", body: JSON.stringify({ org_id: orgId, entry_date: group.date, memo: group.memo, lines: group.lines }) });
      setImportText(""); setNotice(`Imported ${byKey.size} journal(s).`);
      await reload(orgId!);
    });
  }

  async function createAccount(e: FormEvent) {
    e.preventDefault();
    await action(async () => {
      await api("/v1/accounts", { method: "POST", body: JSON.stringify({ ...account, org_id: orgId }) });
      if (active.current !== orgId) return;
      setAccount({ code: "", name: "", type: "expense", class: "expense", description: "", show_on_dashboard: false, show_in_expense_claims: false, enable_payments: false, is_bank_account: false, tax_rate_code: "" }); setNotice("Account saved."); await reload(orgId!);
    });
  }
  async function setTax(id: number, code: string) {
    await action(async () => {
      await api(`/v1/accounts/${id}`, { method: "PUT", body: JSON.stringify({ org_id: orgId, tax_rate_code: code }) });
      if (active.current !== orgId) return;
      setNotice("Default tax rate saved."); await reload(orgId!);
    });
  }
  const amounts = lines.map(l => ({ description: l.description, account_id: Number(l.account_id), debit_cents: l.debit === "" ? 0 : parseCents(l.debit), credit_cents: l.credit === "" ? 0 : parseCents(l.credit) }));
  const debit = amounts.reduce((sum, l) => sum + (l.debit_cents ?? 0), 0);
  const credit = amounts.reduce((sum, l) => sum + (l.credit_cents ?? 0), 0);
  const balanced = debit > 0 && debit === credit && amounts.every(l => l.account_id > 0 && l.debit_cents != null && l.credit_cents != null && (l.debit_cents > 0) !== (l.credit_cents > 0));
  async function post(e: FormEvent) {
    e.preventDefault(); if (!balanced) return;
    await action(async () => {
      key.current ||= crypto.randomUUID();
      const result = await api<{ id: number; reversal_id: number }>("/v1/journals", { method: "POST", body: JSON.stringify({ org_id: orgId, entry_date: date, ...(reverseDate ? { reversal_date: reverseDate } : {}), memo, copy_narration: copyNarration, lines: amounts, idempotency_key: key.current }) });
      if (active.current !== orgId) return;
      setLines([blankLine(), blankLine()]); setMemo(""); setCopyNarration(false); setReverseDate(""); key.current = ""; setNotice(result.reversal_id ? `Journal ${result.id} posted; it reverses on ${reverseDate}.` : `Journal ${result.id} posted.`); await reload(orgId!);
    });
  }
  return <>
    <PageHeader title="Accounting" description="Chart of accounts and posted journals. Posted entries stay in history; manual entries can be reversed." actions={<><button type="button" className="button-secondary" onClick={() => window.print()}>Print / PDF</button><a className="button-secondary" href="/banking/accounts">Bank accounts</a> <a className="button-secondary" href="/accounting/assets">Fixed assets</a> <a className="button-secondary" href="/accounting/period-close">Period close</a></>} />
    <nav className="tabs" aria-label="Accounting sections"><ul><li><button className="button-secondary" onClick={() => selectTab("accounts")} aria-pressed={tab === "accounts"}>Chart of accounts</button></li><li><button className="button-secondary" onClick={() => selectTab("journals")} aria-pressed={tab === "journals"}>Journals</button></li></ul></nav>
    {!selected && <p className="notice">Select a company first.</p>}{error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}{selected && !loaded && !error && <p role="status">Loading accounting records…</p>}
    {selected && loaded && tab === "accounts" && <>
      {canWrite && <form className="record-form" onSubmit={createAccount}><fieldset disabled={busy}><legend>New account</legend><div className="form-grid"><div><label htmlFor="account-code">Account code</label><input id="account-code" required maxLength={20} pattern="[A-Za-z0-9_-]+" value={account.code} onChange={e => setAccount({ ...account, code: e.target.value })} /><p className="section-help">Codes 1000, 1100 and 4000 are reserved for built-in posting rules.</p></div><div><label htmlFor="account-name">Account name</label><input id="account-name" required maxLength={200} value={account.name} onChange={e => setAccount({ ...account, name: e.target.value })} /></div><div><label htmlFor="account-type">Account type</label><select id="account-type" value={account.type} onChange={e => { const type = e.target.value; setAccount({ ...account, type, class: defaultAccountClass(type, account.code) }); }}>{["asset", "liability", "equity", "revenue", "expense"].map(t => <option key={t}>{t}</option>)}</select><p className="section-help">The broad group the reports post with.</p></div><div><label htmlFor="account-class">Account class</label><select id="account-class" value={account.class} onChange={e => setAccount({ ...account, class: e.target.value })}>{classesFor(account.type).map(c => <option key={c.value} value={c.value}>{c.label}</option>)}</select><p className="section-help">Standard chart category inside the type. Options follow the chosen type.</p></div><div><label htmlFor="account-tax">Default tax rate</label><select id="account-tax" value={account.tax_rate_code} onChange={e => setAccount({ ...account, tax_rate_code: e.target.value })}>{TAX_CODES.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select><p className="section-help">A new line on this account starts at this rate. The line can still choose another.</p></div><div><label htmlFor="account-description">Description (optional)</label><textarea id="account-description" maxLength={4000} value={account.description} onChange={e => setAccount({ ...account, description: e.target.value })} /></div><div><label className="check-label"><input type="checkbox" checked={account.show_on_dashboard} onChange={e => setAccount({ ...account, show_on_dashboard: e.target.checked })} />Show on dashboard watchlist</label><label className="check-label"><input type="checkbox" checked={account.show_in_expense_claims} onChange={e => setAccount({ ...account, show_in_expense_claims: e.target.checked })} />Show in expense claims</label><label className="check-label"><input type="checkbox" checked={account.enable_payments} onChange={e => setAccount({ ...account, enable_payments: e.target.checked })} />Enable payments</label><label className="check-label"><input type="checkbox" checked={account.is_bank_account} onChange={e => setAccount({ ...account, is_bank_account: e.target.checked, ...(e.target.checked ? { type: "asset", class: "bank" } : {}) })} />Bank account</label><p className="section-help">Watchlist accounts appear on the overview with their posted balance. Expense claims are not built yet, so that flag is stored but unused; payments have no online provider.</p></div></div></fieldset><button disabled={busy}>Save account</button></form>}
      <p className="form-actions"><button type="button" className="button-secondary" onClick={() => window.print()}>Print chart of accounts</button></p>
      {!accounts.length && <p className="notice">No accounts yet. Invoice posting creates standard bank, receivable and sales accounts. Add other accounts here.</p>}
      {!!accounts.length && <nav className="tabs" aria-label="Chart of accounts classes"><ul>{CLASS_TABS.map(t => <li key={t.id}><button className="button-secondary" aria-pressed={classTab === t.id} onClick={() => setClassTab(t.id)}>{t.label}</button></li>)}</ul></nav>}
      {canWrite && !!accounts.length && <form className="record-form" onSubmit={bulkTaxRate}><fieldset disabled={busy}><legend>Change tax rate in bulk</legend><p className="section-help">Select accounts and apply one default tax rate across multiple accounts at once.</p><label htmlFor="bulk-accounts">Accounts</label><select id="bulk-accounts" multiple size={4} value={bulkIDs.map(String)} onChange={e => setBulkIDs(Array.from(e.target.selectedOptions).map(o => Number(o.value)))}>{accounts.map(a => <option key={a.id} value={a.id}>{a.code} {a.name}</option>)}</select><div className="form-grid"><div><label htmlFor="bulk-tax">Default tax rate</label><select id="bulk-tax" value={bulkTax} onChange={e => setBulkTax(e.target.value)}>{TAX_CODES.map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></div></div><button disabled={busy || !bulkIDs.length}>Apply to {bulkIDs.length} account(s)</button></fieldset></form>}
      {!!accounts.length && <div className="table-scroll"><table className="doc-table"><caption className="sr-only">Chart of accounts</caption><thead><tr><th>Code</th><th>Name</th><th>Class</th><th>Type</th><th>Tax rate</th><th className="num">Year to date</th><th>Options</th></tr></thead><tbody>{accounts.filter(a => classInTab(a.class, classTab)).map(a => <tr key={a.id}><th scope="row">{a.code}</th><td>{a.name}</td><td>{classLabel(a.class)}</td><td>{a.type}</td><td><select aria-label={`Default tax rate for ${a.code} ${a.name}`} disabled={busy || !canWrite} value={a.tax_rate_code ?? ""} onChange={e => setTax(a.id, e.target.value)}>{TAX_CODES.map(([value, label]) => <option key={value} value={value}>{label}</option>)}{a.tax_rate_code && !TAX_CODES.some(([value]) => value === a.tax_rate_code) && <option value={a.tax_rate_code}>{a.tax_rate_code}</option>}</select></td><td className="num">{formatCents(a.ytd_cents)}</td><td>{[a.show_on_dashboard && "Watchlist", a.show_in_expense_claims && "Expense claims", a.enable_payments && "Payments"].filter(Boolean).join(", ") || "—"}{a.class === "bank" && <> · <a href={`/banking/reconcile?account=${a.id}`}>Reconcile {a.name}</a></>}</td></tr>)}</tbody></table></div>}
      {!!accounts.length && !accounts.some(a => classInTab(a.class, classTab)) && <p className="notice">No accounts in this class.</p>}
    </>}
    {selected && loaded && tab === "journals" && <>
      {canWrite && <form className="record-form" onSubmit={post}><fieldset disabled={busy}><legend>Post a manual journal</legend><p className="section-help">Posting changes your books immediately. Enter equal debit and credit totals. Use invoice/payment workflows for customer transactions.</p><div className="form-grid"><div><label htmlFor="journal-date">Entry date</label><DateInput id="journal-date" required value={date} onChange={e => { setDate(e.target.value); key.current = ""; }}/></div><div><label htmlFor="journal-memo">Description</label><input id="journal-memo" maxLength={500} value={memo} onChange={e => { setMemo(e.target.value); key.current = ""; }} /></div><div><label className="check-label"><input type="checkbox" checked={copyNarration} onChange={e => { setCopyNarration(e.target.checked); key.current = ""; }} />Use the description for blank line descriptions</label><p className="section-help">Lines you leave blank get the journal description; typed line descriptions are kept.</p></div><div><label htmlFor="journal-reverse">Auto-reverse on <span className="field-optional">Optional</span></label><DateInput id="journal-reverse" min={date} value={reverseDate} onChange={e => { setReverseDate(e.target.value); key.current = ""; }}/><p className="section-help">Leave blank for a normal journal. With a later date, the reversal is posted on that day with debits and credits swapped.</p></div></div>
      {canWrite && <form className="record-form" onSubmit={importJournals}><fieldset disabled={busy}><legend>Import journals (CSV)</legend><p className="section-help">One row per line: date,narration,account code,debit,credit. Rows with the same date and narration become one journal. Each journal must balance.</p><label htmlFor="journal-import">CSV rows</label><textarea id="journal-import" rows={5} value={importText} onChange={e => setImportText(e.target.value)} placeholder={"2026-01-31,Opening balances,1100,1000.00,\n2026-01-31,Opening balances,3900,,1000.00"} /><button disabled={busy || !importText.trim()}>Import journals</button></fieldset></form>}
      {lines.map((line, index) => <fieldset key={index}><legend>Line {index + 1}</legend><div className="form-grid"><div><label htmlFor={`journal-desc-${index}`}>Description (optional)</label><input id={`journal-desc-${index}`} maxLength={500} value={line.description} onChange={e => { setLines(lines.map((l, i) => i === index ? { ...l, description: e.target.value } : l)); key.current = ""; }} /></div><div><label htmlFor={`journal-account-${index}`}>Account</label><select id={`journal-account-${index}`} required value={line.account_id} onChange={e => { setLines(lines.map((l, i) => i === index ? { ...l, account_id: e.target.value } : l)); key.current = ""; }}><option value="">Choose account</option>{accounts.map(a => <option key={a.id} value={a.id}>{a.code} · {a.name}</option>)}</select></div>{(["debit", "credit"] as const).map(side => <div key={side}><label htmlFor={`journal-${side}-${index}`}>{side === "debit" ? "Debit" : "Credit"} (SGD)</label><input id={`journal-${side}-${index}`} inputMode="decimal" pattern="[0-9]+(\.[0-9]{1,2})?" value={line[side]} onChange={e => { setLines(lines.map((l, i) => i === index ? { ...l, [side]: e.target.value } : l)); key.current = ""; }} /></div>)}</div>{lines.length > 2 && <button type="button" className="button-secondary" onClick={() => { setLines(lines.filter((_, i) => i !== index)); key.current = ""; }}>Remove line {index + 1}</button>}</fieldset>)}
      <div className="form-actions"><button type="button" className="button-secondary" disabled={lines.length >= 200} onClick={() => { setLines([...lines, blankLine()]); key.current = ""; }}>Add line</button></div><p role="status">Debit {formatCents(debit)} · Credit {formatCents(credit)} · {balanced ? "Balanced" : "Not balanced"}</p></fieldset><div className="form-actions"><button disabled={busy || !balanced} type="submit">Post journal</button><button type="button" className="button-secondary" disabled={busy} onClick={() => { setDate(today()); setMemo(""); setCopyNarration(false); setReverseDate(""); setLines([blankLine(), blankLine()]); key.current = ""; setError(""); setNotice(""); }}>Cancel</button></div></form>}
      {detail && <section className="record-form"><h2>Journal {detail.id}</h2><div className="table-scroll"><table className="doc-table"><thead><tr><th>Account</th><th>Description</th><th>Debit</th><th>Credit</th></tr></thead><tbody>{detail.lines.map((l, i) => <tr key={i}><th scope="row">{l.code} · {l.name}</th><td>{l.description}</td><td>{formatCents(l.debit_cents)}</td><td>{formatCents(l.credit_cents)}</td></tr>)}</tbody></table></div><button className="button-secondary" onClick={() => setDetail(null)}>Close details</button></section>}
      {confirm && <section className="record-form notice"><h2>Void journal {confirm.id}?</h2><p>The original entry stays unchanged. A dated reversing entry offsets its amounts and the journal shows as voided. This cannot be undone.</p><label htmlFor="reverse-date">Reversal date</label><DateInput id="reverse-date" min={confirm.entry_date} value={confirmDate} onChange={e => setConfirmDate(e.target.value)}/><div className="form-actions"><button disabled={busy || confirmDate < confirm.entry_date} onClick={() => action(async () => { await api(`/v1/journals/${confirm.id}/void`, { method: "POST", body: JSON.stringify({ org_id: orgId, reversal_date: confirmDate }) }); if (active.current !== orgId) return; setConfirm(null); setNotice("Journal voided."); await reload(orgId!); })}>Confirm void</button><button disabled={busy} className="button-secondary" onClick={() => setConfirm(null)}>Cancel</button></div></section>}
      <nav className="tabs" aria-label="Journal status"><ul>{[["all","All"],["posted","Posted"],["voided","Voided"]].map(([id,label]) => <li key={id}><button type="button" className="button-secondary" aria-pressed={journalFilter === id} onClick={() => setJournalFilter(id)}>{label} ({id === "all" ? journals.length : journals.filter(j => (j.status || "posted") === id).length})</button></li>)}</ul></nav>
      {!journals.length && <p className="notice">No posted journals.</p>}{!!journals.filter(j => journalFilter === "all" || (j.status || "posted") === journalFilter).length && <div className="table-scroll"><table className="doc-table"><caption>Latest 200 posted journals</caption><thead><tr><th>ID</th><th>Date</th><th>Description</th><th>Status</th><th>Amount</th><th>Actions</th></tr></thead><tbody>{journals.filter(j => journalFilter === "all" || (j.status || "posted") === journalFilter).map(j => <tr key={j.id}><th scope="row">{j.id}</th><td>{j.entry_date}</td><td>{j.memo}{j.reversal_of > 0 && ` (reverses ${j.reversal_of})`}{j.reversed_by > 0 && ` (reversed by ${j.reversed_by})`}</td><td>{j.status || "posted"}</td><td>{formatCents(j.total_cents)}</td><td><button className="button-secondary" disabled={busy} onClick={() => action(async () => { const result = await api<{ lines: Line[] }>(`/v1/journals/${j.id}?org_id=${orgId}`); if (active.current === orgId) setDetail({ id: j.id, lines: result.lines }); })}>View</button>{canWrite && j.manual && !j.reversed_by && !j.reversal_of && (j.status || "posted") !== "voided" && <button className="button-secondary" disabled={busy} onClick={() => { setConfirm(j); setConfirmDate(today() < j.entry_date ? j.entry_date : today()); }}>Void</button>}</td></tr>)}</tbody></table></div>}
    </>}
  </>;
}
