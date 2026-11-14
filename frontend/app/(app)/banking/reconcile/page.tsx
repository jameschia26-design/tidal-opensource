"use client";
import Link from "next/link";
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import { formatDate, todayISO } from "@/lib/dates";
import DateInput from "@/components/DateInput";
import { TrackingFields } from "@/components/TrackingSelect";

type Account = { id: number; code: string; name: string };
type Suggestion = { rule_id: number; name: string; direction: string; account_id: number; contact_id: number | null; tax_rate_code: string | null; tracking: Record<string, number> };
type Line = { id: number; date: string; description: string; reference: string; amount_cents: number; status: string; matched_kind: string | null; matched_id: number | null; suggested_account_id: number | null; suggestion: Suggestion | null };
type Candidate = { kind: string; id: number; amount_cents: number; date: string; label: string; days_apart: number };
type Figures = { opening_cents: number; statement_cents: number; ledger_cents: number; difference_cents: number; unmatched_lines: number; lines_total: number };
type Saved = { id: number; account_name: string; from: string; to: string; statement_cents: number; ledger_cents: number; lines_total: number; created_at: string; created_by: string; difference_cents: number };

export default function ReconcilePage() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}
function Workspace() {
  const { selected } = useOrg(); const orgId = selected?.id;
  const canWrite = selected != null && ["owner", "admin", "accountant"].includes(selected.role);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [bankId, setBankId] = useState("");
  const [from, setFrom] = useState(() => todayISO().slice(0, 8) + "01"); const [to, setTo] = useState(todayISO);
  const [lines, setLines] = useState<Line[] | null>(null); const [figures, setFigures] = useState<Figures | null>(null);
  const [openLine, setOpenLine] = useState<number | null>(null); const [candidates, setCandidates] = useState<Candidate[]>([]);
  const [saved, setSaved] = useState<Saved[]>([]);
  const [busy, setBusy] = useState(false); const [error, setError] = useState(""); const [notice, setNotice] = useState("");
  const [kind, setKind] = useState("spend"); const [account, setAccount] = useState(""); const [tracking, setTracking] = useState<Record<string, number>>({});
  const [allAccounts, setAllAccounts] = useState<{ id: number; code: string; name: string; type: string; is_bank_account: boolean }[]>([]);

  useEffect(() => {
    if (orgId == null) return;
    (async () => {
      const [b, a] = await Promise.all([api<{ accounts: Account[] }>(`/v1/bank/accounts?org_id=${orgId}`), api<{ accounts: { id: number; code: string; name: string; type: string; is_bank_account: boolean }[] }>(`/v1/accounts?org_id=${orgId}`)]);
      setAccounts(b.accounts); setAllAccounts(a.accounts);
      const wanted = Number(new URLSearchParams(window.location.search).get("account"));
      const pick = b.accounts.find((x) => x.id === wanted) ?? b.accounts[0];
      if (pick) setBankId(String(pick.id));
    })().catch((e: ApiError) => setError(messageFor(e.error, e.status)));
  }, [orgId]);

  async function load() {
    if (!orgId || !bankId) return;
    setBusy(true); setError(""); setNotice("");
    try {
      const [l, f, r] = await Promise.all([
        api<{ lines: Line[] }>(`/v1/bank/lines?org_id=${orgId}&bank_account_id=${bankId}`),
        api<Figures>(`/v1/bank/reconcile?org_id=${orgId}&bank_account_id=${bankId}&from=${from}&to=${to}`),
        api<{ reconciliations: Saved[] }>(`/v1/bank/reconciliations?org_id=${orgId}&bank_account_id=${bankId}`),
      ]);
      setLines(l.lines.filter((x) => x.date >= from && x.date <= to));
      setFigures(f); setSaved(r.reconciliations);
    } catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } finally { setBusy(false); }
  }
  useEffect(() => { void load(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [orgId, bankId]);

  async function suggest(line: Line) {
    if (!orgId) return;
    setOpenLine(line.id); setCandidates([]); setKind(line.amount_cents > 0 ? "receive" : "spend");
    setAccount(line.suggestion ? String(line.suggestion.account_id) : ""); setTracking(line.suggestion?.tracking ?? {});
    try { setCandidates((await api<{ suggestions: Candidate[] }>(`/v1/bank/lines/${line.id}/suggestions?org_id=${orgId}`)).suggestions); }
    catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); }
  }
  async function match(line: Line, c: Candidate) {
    if (!orgId || busy) return;
    setBusy(true); setError("");
    try { await api(`/v1/bank/lines/${line.id}/match`, { method: "POST", body: JSON.stringify({ org_id: orgId, kind: c.kind, payment_id: c.id }) }); setNotice("Matched. Nothing was moved at your bank."); setOpenLine(null); await load(); }
    catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } finally { setBusy(false); }
  }
  async function create(line: Line) {
    if (!orgId || busy || !account) return;
    setBusy(true); setError("");
    try { await api(`/v1/bank/lines/${line.id}/create`, { method: "POST", body: JSON.stringify({ org_id: orgId, kind, account_id: Number(account), tracking }) }); setNotice("Transaction created and matched to this line."); setOpenLine(null); await load(); }
    catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } finally { setBusy(false); }
  }
  async function saveReconciliation() {
    if (!orgId || busy || !figures) return;
    setBusy(true); setError("");
    try { await api("/v1/bank/reconciliations", { method: "POST", body: JSON.stringify({ org_id: orgId, bank_account_id: Number(bankId), from, to }) }); setNotice("Reconciliation saved."); await load(); }
    catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } finally { setBusy(false); }
  }
  const otherAccounts = allAccounts.filter((a) => a.is_bank_account && String(a.id) !== bankId);
  const pickAccounts = kind === "transfer" ? otherAccounts : allAccounts;

  return <><PageHeader title="Reconcile" description="Match every statement line to something already recorded here, or create the transaction from the line." actions={<><Link href="/banking">Banking</Link> <Link href="/banking/accounts">Bank accounts</Link></>} />
    {!selected && <p>Choose a company first.</p>}{error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    <form className="inline-form" onSubmit={(e) => { e.preventDefault(); void load(); }}>
      <div><label htmlFor="rc-account">Bank account</label><select id="rc-account" required value={bankId} onChange={(e) => setBankId(e.target.value)}>{accounts.map((a) => <option key={a.id} value={a.id}>{a.code} · {a.name}</option>)}{accounts.length === 0 && <option value="">No bank account yet</option>}</select></div>
      <div><label htmlFor="rc-from">From</label><DateInput id="rc-from" required value={from} onChange={(e) => setFrom(e.target.value)} /></div>
      <div><label htmlFor="rc-to">To</label><DateInput id="rc-to" required value={to} onChange={(e) => setTo(e.target.value)} /></div>
      <button disabled={busy || !bankId}>{busy ? "Checking…" : "Reconcile"}</button>
    </form>
    {figures && <section className="kpis" aria-label="Running reconciliation">
      <dl><dt>Opening balance</dt><dd>{formatCents(figures.opening_cents)}</dd></dl>
      <dl><dt>Statement closing</dt><dd>{formatCents(figures.statement_cents)}</dd></dl>
      <dl><dt>Balance in Tidal</dt><dd>{formatCents(figures.ledger_cents)}</dd></dl>
      <dl><dt>Difference</dt><dd aria-live="polite">{formatCents(figures.difference_cents)}</dd></dl>
      <dl><dt>Unmatched lines</dt><dd>{figures.unmatched_lines} of {figures.lines_total}</dd></dl>
    </section>}
    {canWrite && figures && <div className="form-actions"><button disabled={busy || figures.difference_cents !== 0 || figures.unmatched_lines > 0} onClick={() => void saveReconciliation()}>Save reconciliation</button><p className="section-help">Both the difference and the unmatched lines must be zero. Saved reconciliations record the period, balances and who did it.</p></div>}
    {lines === null && <p role="status">Loading statement lines…</p>}
    {lines && lines.length === 0 && <p>No statement lines in this period. Import a CSV under Banking.</p>}
    {lines && lines.length > 0 && <div className="table-scroll"><table className="doc-table"><caption>Statement lines and their suggestions</caption>
      <thead><tr><th>Date</th><th>Description</th><th>Reference</th><th className="num">Amount</th><th>Rule suggestion</th><th>Status</th><th>Action</th></tr></thead>
      <tbody>{lines.map((l) => <tr key={l.id}>
        <td>{formatDate(l.date)}</td><td className="long-text">{l.description}</td><td>{l.reference || "—"}</td><td className="num">{formatCents(l.amount_cents)}</td>
        <td>{l.suggestion ? `${l.suggestion.name} → ${allAccounts.find((a) => a.id === l.suggestion?.account_id)?.name ?? l.suggestion.account_id}${l.suggestion.contact_id ? `, contact ${l.suggestion.contact_id}` : ""}${l.suggestion.tax_rate_code ? `, tax ${l.suggestion.tax_rate_code}` : ""}${Object.keys(l.suggestion.tracking ?? {}).length ? ", tracking set" : ""} (suggestion only)` : l.suggested_account_id ? `Account ${l.suggested_account_id}` : "—"}</td>
        <td>{l.status === "matched" ? `Matched to ${l.matched_kind} ${l.matched_id}` : "Not matched"}</td>
        <td>{l.status === "matched" || !canWrite ? "—" : <button className="button-secondary" disabled={busy} onClick={() => void suggest(l)}>Match or create {l.reference || l.description}</button>}</td>
      </tr>)}</tbody></table></div>}
    {openLine != null && canWrite && <section className="record-form" aria-label="Match or create for the chosen line">
      <h2>Suggestions</h2>
      {candidates.length === 0 ? <p>No recorded payment, receipt, expense or transfer has this amount inside the date tolerance. Create it below.</p>
        : <ul>{candidates.map((c) => <li key={`${c.kind}:${c.id}`}><button className="button-secondary" disabled={busy} onClick={() => void match(lines!.find((x) => x.id === openLine)!, c)}>{c.label} — {formatCents(c.amount_cents)} on {formatDate(c.date)}{c.days_apart === 0 ? "" : ` (${c.days_apart} day(s) apart)`}</button></li>)}</ul>}
      <h2>Create from this line</h2>
      <div className="form-grid">
        <div><label htmlFor="rc-kind">Type</label><select id="rc-kind" value={kind} onChange={(e) => setKind(e.target.value)}><option value="spend">Spend money</option><option value="receive">Receive money</option><option value="transfer">Transfer money</option></select></div>
        <div><label htmlFor="rc-to-account">Account</label><select id="rc-to-account" required value={account} onChange={(e) => setAccount(e.target.value)}><option value="">Choose…</option>{pickAccounts.map((a) => <option key={a.id} value={a.id}>{a.code} · {a.name}</option>)}</select></div>
      </div>
      {orgId != null && <TrackingFields orgId={orgId} idPrefix="rc" lineNumber={1} value={tracking} onChange={setTracking} />}
      <button disabled={busy || !account} onClick={() => { const l = lines!.find((x) => x.id === openLine); if (l) void create(l); }}>Create and match</button>
      <button className="button-secondary" onClick={() => setOpenLine(null)}>Close</button>
    </section>}
    {saved.length > 0 && <div className="table-scroll"><table className="doc-table"><caption>Saved reconciliations</caption>
      <thead><tr><th>Account</th><th>Period</th><th className="num">Statement</th><th className="num">Tidal</th><th className="num">Difference</th><th>Lines</th><th>Saved</th></tr></thead>
      <tbody>{saved.map((r) => <tr key={r.id}><td>{r.account_name}</td><td>{formatDate(r.from)} – {formatDate(r.to)}</td><td className="num">{formatCents(r.statement_cents)}</td><td className="num">{formatCents(r.ledger_cents)}</td><td className="num">{formatCents(r.difference_cents)}</td><td>{r.lines_total}</td><td>{r.created_by} on {formatDate(r.created_at.slice(0, 10))}</td></tr>)}</tbody></table></div>}
  </>;
}
