"use client";
import { useEffect, useRef, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { formatCents, parseCents } from "@/lib/money";
import DateInput from "@/components/DateInput";

type Account = { id: number; code: string; name: string; type: string };
type Row = { account_id: string; debit: string; credit: string };
const blank = (): Row => ({ account_id: "", debit: "", credit: "" });

export default function ConversionBalances() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}
function Workspace() {
  const { selected } = useOrg(); const orgId = selected?.id;
  const [accounts, setAccounts] = useState<Account[]>([]); const [posted, setPosted] = useState<{ id: number; conversion_date: string } | null>(null);
  const [loaded, setLoaded] = useState(false); const [busy, setBusy] = useState(false); const [error, setError] = useState(""); const [notice, setNotice] = useState("");
  const [date, setDate] = useState(""); const [rows, setRows] = useState<Row[]>([blank(), blank()]);
  const [compareDate, setCompareDate] = useState(""); const [compareRows, setCompareRows] = useState<Row[]>([blank(), blank()]); const [showCompare, setShowCompare] = useState(false);
  const done = useRef(false);
  const canWrite = selected && ["owner", "admin", "accountant"].includes(selected.role);
  useEffect(() => {
    if (orgId == null) return; let live = true;
    Promise.all([api<{ accounts: Account[] }>(`/v1/accounts?org_id=${orgId}`), api<{ journal: { id: number; conversion_date: string } | null }>(`/v1/conversion-balances?org_id=${orgId}`)])
      .then(([a, c]) => { if (live) { setAccounts(a.accounts); setPosted(c.journal); setLoaded(true); } })
      .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [orgId]);
  const lines = rows.map(r => ({ account_id: Number(r.account_id), debit_cents: r.debit ? parseCents(r.debit) : 0, credit_cents: r.credit ? parseCents(r.credit) : 0 }));
  const debit = lines.reduce((n, l) => n + (l.debit_cents ?? 0), 0); const credit = lines.reduce((n, l) => n + (l.credit_cents ?? 0), 0);
  const gridOK = (ls: typeof lines) => ls.length > 0 && ls.every(l => l.account_id > 0 && l.debit_cents !== null && l.credit_cents !== null && ((l.debit_cents ?? 0) > 0) !== ((l.credit_cents ?? 0) > 0));
  const compareLines = compareRows.map(r => ({ account_id: Number(r.account_id), debit_cents: r.debit ? parseCents(r.debit) : 0, credit_cents: r.credit ? parseCents(r.credit) : 0 }));
  const compareDebit = compareLines.reduce((n, l) => n + (l.debit_cents ?? 0), 0); const compareCredit = compareLines.reduce((n, l) => n + (l.credit_cents ?? 0), 0);
  // A comparative date must sit before the conversion date, so the two opening journals read in order.
  const valid = !!date && gridOK(lines) && (!showCompare || (!!compareDate && compareDate < date && gridOK(compareLines)));
  const setCompare = (i: number, patch: Partial<Row>) => setCompareRows(compareRows.map((r, j) => j === i ? { ...r, ...patch } : r));
  const set = (i: number, patch: Partial<Row>) => setRows(rows.map((r, j) => j === i ? { ...r, ...patch } : r));
  async function save(e: FormEvent) {
    e.preventDefault(); if (!orgId || !valid || busy || done.current) return;
    setBusy(true); setError(""); setNotice("");
    try {
      const r = await api<{ id: number }>("/v1/conversion-balances", { method: "POST", body: JSON.stringify({ org_id: orgId, conversion_date: date, lines,
        ...(showCompare ? { comparative_date: compareDate, comparative_lines: compareLines } : {}) }) });
      done.current = true; setPosted({ id: r.id, conversion_date: date }); setNotice("Conversion balances posted.");
    } catch (x) { const err = x as ApiError; setError(messageFor(err.error, err.status)); } finally { setBusy(false); }
  }
  return <>
    <PageHeader title="Conversion balances" description="Opening balances as at your conversion date. Posts once as a single journal; any difference goes to Opening Balance Equity." />
    {!selected && <p className="notice">Choose a company first.</p>}{error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {selected && !loaded && !error && <p role="status">Loading…</p>}
    {loaded && posted && <p className="notice">Conversion balances were posted as journal {posted.id} dated {posted.conversion_date}. Correct them with a manual journal.</p>}
    {loaded && !posted && !canWrite && <p className="notice">Only company owners, admins and accountants can enter conversion balances.</p>}
    {loaded && !posted && canWrite && <form className="record-form" onSubmit={save}><fieldset disabled={busy}><legend>Account balances</legend>
      <p className="section-help">Posting changes your books and cannot be edited afterwards. Enter each balance as a debit or a credit. Do not enter receivables or payables here; set those on each customer and supplier as opening balances.</p>
      <div><label htmlFor="conv-date">Conversion date</label><DateInput id="conv-date" required value={date} onChange={e => setDate(e.target.value)}/></div>
      {rows.map((r, i) => <fieldset key={i}><legend>Line {i + 1}</legend><div className="form-grid">
        <div><label htmlFor={`conv-account-${i}`}>Account</label><select id={`conv-account-${i}`} required value={r.account_id} onChange={e => set(i, { account_id: e.target.value })}><option value="">Choose account</option>{accounts.map(a => <option key={a.id} value={a.id}>{a.code} · {a.name}</option>)}</select></div>
        {(["debit", "credit"] as const).map(side => <div key={side}><label htmlFor={`conv-${side}-${i}`}>{side === "debit" ? "Debit" : "Credit"} (SGD)</label><input id={`conv-${side}-${i}`} inputMode="decimal" pattern="[0-9]+(\.[0-9]{1,2})?" value={r[side]} onChange={e => set(i, { [side]: e.target.value })} /></div>)}
      </div>{rows.length > 1 && <button type="button" className="button-secondary" onClick={() => setRows(rows.filter((_, j) => j !== i))}>Remove line {i + 1}</button>}</fieldset>)}
      <button type="button" className="button-secondary" disabled={rows.length >= 200} onClick={() => setRows([...rows, blank()])}>Add a new line</button>
      <button type="button" className="button-secondary" onClick={() => setRows(accounts.filter(a => ["asset", "liability", "equity"].includes(a.type)).map(a => ({ account_id: String(a.id), debit: "", credit: "" })))}>Show all accounts</button>
      <button type="button" className="button-secondary" onClick={() => setShowCompare(!showCompare)}>{showCompare ? "Remove comparative balances" : "Add Comparative Balances"}</button>
      {showCompare && <fieldset><legend>Comparative balances</legend><p className="section-help">Opening balances at an earlier date, usually last year&apos;s close, posted as a second journal so reports that compare years have both sides.</p>
        <div><label htmlFor="conv-compare-date">Comparative date</label><DateInput id="conv-compare-date" required value={compareDate} max={date || undefined} onChange={e => setCompareDate(e.target.value)}/></div>
        {compareRows.map((r, i) => <fieldset key={i}><legend>Comparative line {i + 1}</legend><div className="form-grid">
          <div><label htmlFor={`conv-compare-account-${i}`}>Account</label><select id={`conv-compare-account-${i}`} required value={r.account_id} onChange={e => setCompare(i, { account_id: e.target.value })}><option value="">Choose account</option>{accounts.map(a => <option key={a.id} value={a.id}>{a.code} · {a.name}</option>)}</select></div>
          {(["debit", "credit"] as const).map(side => <div key={side}><label htmlFor={`conv-compare-${side}-${i}`}>{side === "debit" ? "Debit" : "Credit"} (SGD)</label><input id={`conv-compare-${side}-${i}`} inputMode="decimal" pattern="[0-9]+(\.[0-9]{1,2})?" value={r[side]} onChange={e => setCompare(i, { [side]: e.target.value })} /></div>)}
        </div>{compareRows.length > 1 && <button type="button" className="button-secondary" onClick={() => setCompareRows(compareRows.filter((_, j) => j !== i))}>Remove comparative line {i + 1}</button>}</fieldset>)}
        <button type="button" className="button-secondary" disabled={compareRows.length >= 200} onClick={() => setCompareRows([...compareRows, blank()])}>Add a comparative line</button>
        <p role="status">Comparative debits {formatCents(compareDebit)} · Comparative credits {formatCents(compareCredit)} · Difference to Opening Balance Equity {formatCents(Math.abs(compareDebit - compareCredit))}</p>
      </fieldset>}
      <button type="button" className="button-secondary" onClick={() => { setRows([blank(), blank()]); setDate(""); setCompareRows([blank(), blank()]); setCompareDate(""); setShowCompare(false); setError(""); setNotice(""); }}>Cancel</button>
      <p role="status">Total debits {formatCents(debit)} · Total credits {formatCents(credit)} · Difference to Opening Balance Equity {formatCents(Math.abs(debit - credit))}</p>
    </fieldset><button disabled={busy || !valid} type="submit">Save</button></form>}
  </>;
}
