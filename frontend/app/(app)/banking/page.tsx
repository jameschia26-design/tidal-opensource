"use client";
import Link from "next/link";
import { useEffect, useRef, useState } from "react";
import PageHeader from "@/components/PageHeader";
import PreviewNotice from "@/components/PreviewNotice";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { formatCents } from "@/lib/money";
import { BANK_STATEMENT_HEADERS, csvCell, csvTemplate, downloadCsv, parseCsv } from "@/lib/csv";
import { messageFor } from "@/lib/errors";
import { formatDate } from "@/lib/dates";

// Bank statements are imported from a CSV file you download from your bank. Tidal never connects to a bank.
type Line = { id: number; date: string; description: string; reference: string; amount_cents: number; matched_kind: string | null; matched_id: number | null; suggested_account_id: number | null };
type Candidate = { kind: "invoice" | "bill"; id: number; label: string; amount_cents: number; date: string };
type BankAccount = { id: number; code: string; name: string; archived: boolean };

// Same limits the statement importer applies, checked here so one bad line is named instead of rejecting the file.
function statementLineProblem(r: string[]): string | null {
  const [date = "", description = "", reference = "", amount = ""] = r;
  if (r.length !== 4) return "each line needs four columns: date, description, reference, amount";
  if (!/^\d{4}-\d{2}-\d{2}$/.test(date) || new Date(`${date}T00:00:00Z`).toISOString().slice(0, 10) !== date) return "date must be a real date as YYYY-MM-DD";
  if (!description) return "description is required";
  if (description.length > 300) return "description is limited to 300 characters";
  if (reference.length > 100) return "reference is limited to 100 characters";
  if (!/^-?\d{1,12}(\.\d{1,2})?$/.test(amount) || Number(amount) === 0) return "amount must be a non-zero SGD amount with up to two decimals, negative for money out";
  return null;
}

export default function BankingPage() {
  const { selected } = useOrg();
  return <BankingWorkspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function BankingWorkspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const live = useRef(true);
  const [lines, setLines] = useState<Line[] | null>(null);
  const [candidates, setCandidates] = useState<Candidate[]>([]);
  const [file, setFile] = useState<File | null>(null);
  const [banks, setBanks] = useState<BankAccount[]>([]);
  const [bankId, setBankId] = useState("");
  const [cols, setCols] = useState({ date: "1", description: "2", reference: "3", amount: "4" });
  const [pick, setPick] = useState<Record<number, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [skipped, setSkipped] = useState<string[]>([]);

  async function load() {
    if (orgId == null) return;
    const [r, b] = await Promise.all([
      api<{ lines: Line[] }>(`/v1/bank/lines?org_id=${orgId}${bankId ? `&bank_account_id=${bankId}` : ""}`),
      api<{ accounts: BankAccount[] }>(`/v1/bank/accounts?org_id=${orgId}`),
    ]);
    if (!live.current) return;
    setLines(r.lines);
    setBanks(b.accounts);
    setBankId((current) => current || String((b.accounts.find((x) => !x.archived) ?? b.accounts[0])?.id ?? ""));
    // Candidate payments: invoice payments (money in) and bill payments (money out), listed from existing records.
    const [inv, bills] = await Promise.all([
      api<{ invoices: { id: number; number: number | null; status: string }[] }>(`/v1/invoices?org_id=${orgId}&status=posted`),
      api<{ bills: { id: number; supplier_reference: string; status: string }[] }>(`/v1/bills?org_id=${orgId}&status=posted`),
    ]);
    if (!live.current) return;
    const list: Candidate[] = [];
    for (const i of inv.invoices) {
      const detail = await api<{ payments: { id: number; amount_cents: number; payment_date: string }[] }>(`/v1/invoices/${i.id}?org_id=${orgId}`);
      for (const p of detail.payments) list.push({ kind: "invoice", id: p.id, label: `Invoice ${i.number ?? i.id} receipt`, amount_cents: p.amount_cents, date: p.payment_date });
    }
    for (const b of bills.bills) {
      const detail = await api<{ payments: { id: number; amount_cents: number; payment_date: string }[] }>(`/v1/bills/${b.id}?org_id=${orgId}`);
      for (const p of detail.payments) list.push({ kind: "bill", id: p.id, label: `Bill ${b.supplier_reference} payment`, amount_cents: p.amount_cents, date: p.payment_date });
    }
    setCandidates(list);
  }
  useEffect(() => {
    live.current = true;
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function importFile() {
    if (!file || busy || orgId == null || !canWrite) return;
    setBusy(true); setError(""); setNotice(""); setSkipped([]);
    try {
      const rows = parseCsv(await file.text());
      // Column mapping: the four 1-based positions the user chose, in date/description/reference/amount order.
      const map = [Number(cols.date), Number(cols.description), Number(cols.reference), Number(cols.amount)];
      const defaultMap = map.every((c, i) => c === i + 1);
      if (map.some((c) => !Number.isInteger(c) || c < 1 || c > 20) || new Set(map).size !== 4) {
        setError("Choose four different column numbers between 1 and 20.");
        return;
      }
      if (defaultMap) {
        const head = (rows[0] ?? []).map((h) => h.toLowerCase());
        if (head.join(",") !== BANK_STATEMENT_HEADERS.join(",")) {
          setError(`The header row must be: ${BANK_STATEMENT_HEADERS.join(", ")}. Map columns below if your bank uses a different order.`);
          return;
        }
      }
      if (rows.length < 2) { setError("The file needs a header row and at least one statement line."); return; }
      // Bad lines are listed and left out; the rest of the file still imports. Nothing is written until the server accepts.
      const lines: string[] = [BANK_STATEMENT_HEADERS.join(",")];
      const bad: string[] = [];
      for (let i = 1; i < rows.length; i++) {
        const row = defaultMap ? rows[i] : map.map((c) => rows[i][c - 1] ?? "");
        const why = statementLineProblem(row);
        if (why) bad.push(`row ${i + 1}: ${why}`);
        else lines.push(row.map(csvCell).join(","));
      }
      setSkipped(bad);
      if (lines.length === 1) { setError("No statement line could be imported. Fix the rows listed and try again."); return; }
      const account = bankId ? `&bank_account_id=${bankId}` : "";
      const res = await fetch(`/api/v1/bank/statements?org_id=${orgId}${account}`, { method: "POST", headers: { "content-type": "text/csv" }, body: lines.join("\n") + "\n", credentials: "same-origin", cache: "no-store" });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) throw { error: typeof body.error === "string" ? body.error : "request_failed", status: res.status };
      if (!live.current) return;
      setNotice(`Imported ${body.imported} new line(s). ${body.duplicates_skipped} already imported were skipped.${bad.length ? ` ${bad.length} row(s) were not sent.` : ""}`);
      setFile(null);
      await load();
    } catch (e) {
      if (live.current) { const x = e as ApiError; setError(messageFor(x.error, x.status)); }
    } finally {
      if (live.current) setBusy(false);
    }
  }

  async function match(line: Line) {
    const value = pick[line.id];
    if (!value || busy || orgId == null) return;
    const [kind, id] = value.split(":");
    setBusy(true); setError(""); setNotice("");
    try {
      await api(`/v1/bank/lines/${line.id}/match`, { method: "POST", body: JSON.stringify({ org_id: orgId, kind, payment_id: Number(id) }) });
      if (!live.current) return;
      setNotice("Matched. Nothing was moved in your bank.");
      await load();
    } catch (e) {
      if (live.current) { const x = e as ApiError; setError(messageFor(x.error, x.status)); }
    } finally {
      if (live.current) setBusy(false);
    }
  }

  const unmatched = (line: Line) => line.matched_kind == null;
  const options = (line: Line) => candidates.filter((c) => (line.amount_cents > 0 ? c.kind === "invoice" : c.kind === "bill") && c.amount_cents === Math.abs(line.amount_cents) && c.date === line.date);

  return (
    <>
      <PageHeader title="Banking" description="Import a bank statement file and match each line to a payment already recorded." />
      <PreviewNotice>Tidal does not connect to your bank and cannot move money. Matching only links a line to a payment already recorded here. Live bank feeds are not connected; <Link href="/settings/integrations">see integration status</Link>.</PreviewNotice>
      {!selected && <p>Choose a company first.</p>}
      {error && <p role="alert" className="form-error">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      {skipped.length > 0 && <ul aria-live="polite">{skipped.map((s, i) => <li key={i}>{s}</li>)}</ul>}
      {selected && canWrite && (
        <form className="record-form" onSubmit={(e) => { e.preventDefault(); void importFile(); }}>
          <h2>Import statement file</h2>
          <fieldset disabled={busy}>
            <legend>CSV file</legend>
            <p className="section-help">Columns in order: date (YYYY-MM-DD), description, reference, amount in SGD (negative for money out). Up to 2,000 rows. Importing the same file twice adds nothing new. Rows that fail are listed; the rest still import.</p>
            <label htmlFor="bank-account-pick">Bank account</label>
            <select id="bank-account-pick" required value={bankId} disabled={busy} onChange={(e) => { setBankId(e.target.value); }}>
              {banks.length === 0 && <option value="">Add a bank account first</option>}
              {banks.map((b) => <option key={b.id} value={b.id}>{b.code} · {b.name}{b.archived ? " (archived)" : ""}</option>)}
            </select>
            <p className="section-help">The statement belongs to one account. <Link href="/banking/accounts">Manage bank accounts</Link>.</p>
            <fieldset><legend>Map columns (1 = first column in the file)</legend>
              <div className="form-grid">
                <div><label htmlFor="col-date">Date</label><input id="col-date" type="number" min={1} max={20} value={cols.date} onChange={(e) => setCols({ ...cols, date: e.target.value })} /></div>
                <div><label htmlFor="col-description">Description</label><input id="col-description" type="number" min={1} max={20} value={cols.description} onChange={(e) => setCols({ ...cols, description: e.target.value })} /></div>
                <div><label htmlFor="col-reference">Reference</label><input id="col-reference" type="number" min={1} max={20} value={cols.reference} onChange={(e) => setCols({ ...cols, reference: e.target.value })} /></div>
                <div><label htmlFor="col-amount">Amount</label><input id="col-amount" type="number" min={1} max={20} value={cols.amount} onChange={(e) => setCols({ ...cols, amount: e.target.value })} /></div>
              </div>
              <p className="section-help">The first row is treated as a header. Negative amounts are money out.</p>
            </fieldset>
            <label htmlFor="bank-file">Choose a CSV file</label>
            <input id="bank-file" type="file" accept=".csv,text/csv" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
            <button type="button" className="button-secondary" disabled={busy} onClick={() => downloadCsv("bank-statement-template.csv", csvTemplate(BANK_STATEMENT_HEADERS))}>Download template</button>
            <button disabled={!file || busy || !bankId}>{busy ? "Importing…" : "Import"}</button>
            <Link href="/banking/reconcile">Reconcile this statement</Link>
          </fieldset>
        </form>
      )}
      {lines === null && !error && <p role="status">Loading statement lines…</p>}
      {lines && lines.length === 0 && <p>No statement lines imported yet.</p>}
      {lines && lines.length > 0 && (
        <div className="table-scroll"><table className="doc-table">
          <caption>Latest 500 statement lines</caption>
          <thead><tr><th>Date</th><th>Description</th><th>Reference</th><th className="num">Amount</th><th>Matched to</th><th>Rule suggestion</th>{canWrite && <th>Match</th>}</tr></thead>
          <tbody>
            {lines.map((l) => (
              <tr key={l.id}>
                <td>{formatDate(l.date)}</td><td className="long-text">{l.description}</td><td>{l.reference || "—"}</td>
                <td className="num">{formatCents(l.amount_cents)}</td>
                <td>{l.matched_kind ? `${l.matched_kind} payment ${l.matched_id}` : "Not matched"}</td><td>{l.suggested_account_id ? `Account ${l.suggested_account_id} (suggestion only)` : "—"}</td>
                {canWrite && (
                  <td>
                    {unmatched(l) ? (
                      <>
                        <label htmlFor={`match-${l.id}`}>Payment</label>
                        <select id={`match-${l.id}`} value={pick[l.id] ?? ""} disabled={busy} onChange={(e) => setPick({ ...pick, [l.id]: e.target.value })}>
                          <option value="">Choose…</option>
                          {options(l).map((c) => <option key={`${c.kind}:${c.id}`} value={`${c.kind}:${c.id}`}>{c.label}</option>)}
                        </select>
                        {options(l).length === 0 && <p className="section-help">No recorded payment with this amount and date.</p>}
                        <button className="button-secondary" disabled={busy || !pick[l.id]} onClick={() => match(l)}>Match</button>
                      </>
                    ) : "Done"}
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table></div>
      )}
    </>
  );
}
