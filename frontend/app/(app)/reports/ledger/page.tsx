"use client";
import { useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";
import { formatDate } from "@/lib/dates";

// One screen for the ledger family: Account Transactions / General Ledger Detail / Journal Report (lines),
// General Ledger Summary and Trial Balance by Date Range (summary).
type Line = { journal_id: number; date: string; memo: string; code: string; account: string; description: string; debit_cents: number; credit_cents: number };
type Sum = { account_id: number; code: string; name: string; type: string; opening_cents: number; debit_cents: number; credit_cents: number; net_cents: number; closing_cents: number };
type Account = { id: number; code: string; name: string };
const DATE = /^\d{4}-\d{2}-\d{2}$/;
const signed = (n: number) => (n < 0 ? `(${formatCents(-n)})` : formatCents(n));

export default function LedgerReports() {
  const orgId = useOrgId();
  return <LedgerWorkspace key={orgId ?? "none"} orgId={orgId} />;
}

function LedgerWorkspace({ orgId }: { orgId: number | null }) {
  const [view, setView] = useState<"lines" | "summary">("lines");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [accountId, setAccountId] = useState("");
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [lines, setLines] = useState<Line[] | null>(null);
  const [sums, setSums] = useState<Sum[] | null>(null);
  const [truncated, setTruncated] = useState(false);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function loadAccounts() {
    if (orgId == null || accounts.length) return;
    try { setAccounts((await api<{ accounts: Account[] }>(`/v1/accounts?org_id=${orgId}`)).accounts); } catch { /* optional filter */ }
  }
  async function run(e: React.FormEvent) {
    e.preventDefault();
    if (orgId == null) return;
    if (!DATE.test(from) || !DATE.test(to)) { setError("Choose a start and end date."); return; }
    setBusy(true); setError(""); setLines(null); setSums(null);
    try {
      const q = `org_id=${orgId}&from=${from}&to=${to}`;
      if (view === "lines") {
        const r = await api<{ lines: Line[]; truncated: boolean }>(`/v1/reports/ledger-lines?${q}${accountId ? `&account_id=${accountId}` : ""}`);
        setLines(r.lines); setTruncated(r.truncated);
      } else {
        setSums((await api<{ rows: Sum[] }>(`/v1/reports/ledger-summary?${q}${accountId ? `&account_id=${accountId}` : ""}`)).rows);
      }
    } catch (err) { setError(messageFor((err as ApiError).error, (err as ApiError).status)); }
    finally { setBusy(false); }
  }

  function clearResults() { setLines(null); setSums(null); setTruncated(false); }
  const filters = <form onSubmit={run} className="inline-form" noValidate>
    <div><label htmlFor="lg-view">Report</label><select id="lg-view" disabled={busy} value={view} onChange={e => { setView(e.target.value as "lines" | "summary"); clearResults(); }}><option value="lines">Account transactions / general ledger detail</option><option value="summary">General ledger summary / trial balance by date range</option></select></div>
    <div><label htmlFor="lg-from">From</label><DateInput id="lg-from" value={from} disabled={busy} onChange={e => { setFrom(e.target.value); clearResults(); }}/></div>
    <div><label htmlFor="lg-to">To</label><DateInput id="lg-to" value={to} disabled={busy} onChange={e => { setTo(e.target.value); clearResults(); }}/></div>
    <div><label htmlFor="lg-acct">Account</label><select id="lg-acct" value={accountId} disabled={busy} onFocus={() => void loadAccounts()} onChange={e => { setAccountId(e.target.value); clearResults(); }}><option value="">All accounts</option>{accounts.map(a => <option key={a.id} value={a.id}>{a.code} — {a.name}</option>)}</select></div>
    <button type="submit" disabled={busy || orgId == null}>{busy ? "Running…" : "Update"}</button>
  </form>;

  return <ReportFrame title="Ledger reports" description="Posted journal lines only. Range up to 366 days." filters={filters}>
    {orgId == null && <p className="muted">Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}
    {lines && <>{!lines.length && <p>Nothing to show here.</p>}
      {!!lines.length && <div className="table-scroll"><table className="doc-table"><caption className="sr-only">Journal lines</caption>
        <thead><tr><th scope="col">Date</th><th scope="col">Account</th><th scope="col">Description</th><th scope="col">Journal memo</th><th scope="col" className="num">Debit</th><th scope="col" className="num">Credit</th></tr></thead>
        <tbody>{lines.map((l, i) => <tr key={i}><td>{formatDate(l.date)}</td><td>{l.code} {l.account}</td><td>{l.description}</td><td>{l.memo}</td><td className="num">{l.debit_cents ? formatCents(l.debit_cents) : ""}</td><td className="num">{l.credit_cents ? formatCents(l.credit_cents) : ""}</td></tr>)}</tbody></table></div>}
      {truncated && <p role="status" className="form-error">Showing the first 5,000 lines. Narrow the dates or choose an account.</p>}</>}
    {sums && <>{!sums.length && <p>Nothing to show here.</p>}
      {!!sums.length && <div className="table-scroll"><table className="doc-table"><caption className="sr-only">Account movements</caption>
        <thead><tr><th scope="col">Account</th><th scope="col">Account type</th><th scope="col" className="num">Opening</th><th scope="col" className="num">Debit</th><th scope="col" className="num">Credit</th><th scope="col" className="num">Net movement</th><th scope="col" className="num">Closing</th></tr></thead>
        <tbody>{sums.map(s => <tr key={s.account_id}><td>{s.code} {s.name}</td><td>{s.type}</td><td className="num">{signed(s.opening_cents)}</td><td className="num">{formatCents(s.debit_cents)}</td><td className="num">{formatCents(s.credit_cents)}</td><td className="num">{signed(s.net_cents)}</td><td className="num">{signed(s.closing_cents)}</td></tr>)}</tbody></table></div>}
      <p className="muted">Debit balances positive; credit balances in brackets.</p></>}
  </ReportFrame>;
}
