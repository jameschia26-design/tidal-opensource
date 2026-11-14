"use client";
import { useEffect, useState } from "react";
import ReportFrame from "@/components/ReportFrame";
import { useOrgId } from "@/components/useOrgId";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Bucket = { lines: number; in_cents: number; out_cents: number };
type Data = {
  matched: Bucket;
  unmatched: Bucket;
  ledger_balance_cents: number;
  statement_closing_cents: number;
  difference_cents: number;
  matched_documents: number;
  account_code: string;
  bank_account_id: number;
};
type Account = { id: number; code: string; name: string; type: string; is_bank_account: boolean };

export default function BankReconciliation() {
  const orgId = useOrgId();
  return <Workspace key={orgId ?? "none"} orgId={orgId} />;
}

function Workspace({ orgId }: { orgId: number | null }) {
  const [from, setFrom] = useState(""), [to, setTo] = useState("");
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [bankId, setBankId] = useState("");
  const [data, setData] = useState<Data | null>(null), [busy, setBusy] = useState(false), [error, setError] = useState("");

  useEffect(() => {
    if (orgId == null) { setAccounts([]); return; }
    let live = true;
    api<{ accounts: Account[] }>(`/v1/accounts?org_id=${orgId}`)
      .then((r) => {
        if (!live) return;
        const banks = r.accounts.filter((a) => a.is_bank_account);
        setAccounts(banks);
        setBankId((current) => current || (banks[0] ? String(banks[0].id) : ""));
      })
      .catch(() => { if (live) setError("Could not load bank accounts."); });
    return () => { live = false; };
  }, [orgId]);

  async function run(e: React.FormEvent) {
    e.preventDefault();
    if (!orgId || busy || !bankId) return;
    setData(null); setError(""); setBusy(true);
    try {
      setData(await api<Data>(`/v1/reports/bank-reconciliation?org_id=${orgId}&from=${from}&to=${to}&bank_account_id=${bankId}`));
    } catch (err) {
      const x = err as ApiError;
      setError(messageFor(x.error, x.status));
    } finally { setBusy(false); }
  }

  const filters = (
    <form onSubmit={run} className="inline-form">
      <div>
        <label htmlFor="br-account">Bank account</label>
        <select id="br-account" required disabled={busy} value={bankId} onChange={(e) => setBankId(e.target.value)}>
          {accounts.length === 0 && <option value="">No bank account flagged yet</option>}
          {accounts.map((a) => <option key={a.id} value={a.id}>{a.code} {a.name}</option>)}
        </select>
      </div>
      <div><label htmlFor="br-from">From</label><DateInput id="br-from" required disabled={busy} value={from} onChange={(e) => setFrom(e.target.value)} /></div>
      <div><label htmlFor="br-to">To</label><DateInput id="br-to" required disabled={busy} value={to} onChange={(e) => setTo(e.target.value)} /></div>
      <button type="submit" disabled={busy || !bankId}>{busy ? "Checking…" : "Reconcile"}</button>
    </form>
  );

  return (
    <ReportFrame title="Bank reconciliation" description="One bank account at a time: imported lines, matched documents and the ledger." filters={filters}>
      <p className="notice">
        Each statement file is imported against the bank account you choose. Import and match under Banking,
        then reconcile here: after every line is matched, the statement closing balance and the ledger balance agree.
      </p>
      {!orgId && <p>Choose a company first.</p>}
      {error && <p role="alert">{error}</p>}
      {orgId && accounts.length === 0 && <p>Flag an account as a bank account in Accounting → Chart of accounts first.</p>}
      {data && (
        <div className="table-scroll">
          <table className="doc-table">
            <caption>Statement lines on account {data.account_code}</caption>
            <thead><tr><th>Status</th><th className="num">Lines</th><th className="num">Money in</th><th className="num">Money out</th></tr></thead>
            <tbody>
              <tr><td>Matched</td><td className="num">{data.matched.lines}</td><td className="num">{formatCents(data.matched.in_cents)}</td><td className="num">{formatCents(data.matched.out_cents)}</td></tr>
              <tr><td>Unmatched</td><td className="num">{data.unmatched.lines}</td><td className="num">{formatCents(data.unmatched.in_cents)}</td><td className="num">{formatCents(data.unmatched.out_cents)}</td></tr>
              <tr><th scope="row">Matched documents</th><td colSpan={3} className="num">{data.matched_documents}</td></tr>
              <tr><th scope="row">Statement closing balance</th><td colSpan={3} className="num">{formatCents(data.statement_closing_cents)}</td></tr>
              <tr><th scope="row">Bank ledger balance to end date</th><td colSpan={3} className="num">{formatCents(data.ledger_balance_cents)}</td></tr>
              <tr><th scope="row">Difference</th><td colSpan={3} className="num">{formatCents(data.difference_cents)}</td></tr>
            </tbody>
          </table>
        </div>
      )}
    </ReportFrame>
  );
}
