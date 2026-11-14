"use client";
import { useEffect, useState } from "react";
import PageHeader from "@/components/PageHeader";
import RecordImport from "@/components/RecordImport";
import { useOrg } from "@/components/OrgProvider";
import { loadLookups } from "@/lib/import-lookups";
import { importSpecs, type LookupMaps } from "@/lib/imports";
import { convert, importKind, label, type Entity, type Mapped, type Table } from "@/lib/migrate";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { todayISO } from "@/lib/dates";

/** Move from another system: upload an export, check what it holds, then import.
 *  Conversion is deterministic — the same file always gives the same rows, and a
 *  rejected row says why. Posting reuses the record importers, so server rules apply. */
export default function MigrationWorkspace() {
  const { selected } = useOrg();
  if (!selected) return <p className="notice">Choose a company to import into.</p>;
  if (selected.role === "viewer") return <p className="notice">Company viewers cannot import.</p>;
  return <Workspace key={selected.id} orgId={selected.id} />;
}

function Workspace({ orgId }: { orgId: number }) {
  const [fileName, setFileName] = useState("");
  const [text, setText] = useState("");
  const [table, setTable] = useState<Table | null>(null);
  const [mapped, setMapped] = useState<Mapped | null>(null);
  const [error, setError] = useState("");
  const [done, setDone] = useState("");

  function run(body: string, as?: Entity) {
    setError(""); setDone("");
    try {
      const c = convert(body, as);
      setTable(c.table); setMapped(c.mapped);
    } catch (e) {
      setTable(null); setMapped(null);
      setError(e instanceof Error ? e.message : "The file could not be read.");
    }
  }

  async function onFile(f: File | null) {
    setTable(null); setMapped(null); setFileName(f?.name ?? ""); setText("");
    setError(""); setDone("");
    if (!f) return;
    const body = await f.text();
    setText(body);
    run(body);
  }

  return (
    <div>
      <h2>Move from another system</h2>
      <p className="section-help">Upload an export from QuickBooks, MYOB, Sage, Peachtree or standard CSV. Nothing posts until the Import step.</p>
      {error && <p role="alert" className="form-error">{error}</p>}
      <section className="record-form" aria-label="Upload export">
        <label htmlFor="migrate-file">Export file (CSV, TXT or IIF)</label>
        <input id="migrate-file" type="file" accept=".csv,.txt,.iif,text/csv,text/plain" onChange={(e) => void onFile(e.target.files?.[0] ?? null)} />
        {fileName && <p className="muted">{fileName}</p>}
        {text && (
          <>
            <label htmlFor="migrate-entity">What is in this file?</label>
            <p className="section-help">Some exports are identical whichever list they came from — a MYOB card file looks the same for customers and suppliers. Change this if the guess is wrong.</p>
            <select id="migrate-entity" value={table?.entity ?? ""} onChange={(e) => run(text, e.target.value as Entity)}>
              {table && <option value={table.entity}>{entityLabel(table.entity)}</option>}
              {ENTITY_OPTIONS.filter((o) => o.value !== table?.entity).map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
            </select>
          </>
        )}
      </section>
      {table && mapped && (
        <Review table={table} mapped={mapped} orgId={orgId} onDone={async () => setDone("Import finished. Check the lists for what landed and what still needs attention.")} />
      )}
      {done && <p className="notice">{done}</p>}
    </div>
  );
}

// Read-only evidence first: every mapped row, every rejection reason, every dropped column.
function Review({ table, mapped, orgId, onDone }: { table: Table; mapped: Mapped; orgId: number; onDone: () => Promise<void> }) {
  const columns = Object.keys(mapped.rows[0] ?? {});
  return (
    <section aria-label="Review converted rows">
      <h2>Detected: {label(table.source)} · {entityLabel(table.entity)}</h2>
      <p className="section-help">
        {mapped.rows.length} rows ready, {mapped.rejected.length} rejected.
        {mapped.unmapped.length > 0 && <> Columns this import ignores: {mapped.unmapped.join(", ")}.</>}
      </p>
      {mapped.rejected.length > 0 && (
        <table>
          <caption>Rejected rows and why</caption>
          <thead><tr><th>Row</th><th>Reason</th></tr></thead>
          <tbody>{mapped.rejected.map((r) => <tr key={r.row}><td>{r.row}</td><td>{r.reason}</td></tr>)}</tbody>
        </table>
      )}
      {mapped.rows.length > 0 && (
        <>
          <table>
            <caption>Rows that will import</caption>
            <thead><tr>{columns.map((h) => <th key={h}>{h}</th>)}</tr></thead>
            <tbody>{mapped.rows.slice(0, 25).map((r, i) => <tr key={i}>{columns.map((h) => <td key={h}>{r[h]}</td>)}</tr>)}</tbody>
          </table>
          {mapped.rows.length > 25 && <p className="muted">Showing the first 25 of {mapped.rows.length} rows.</p>}
          <MigrationImport table={table} mapped={mapped} orgId={orgId} onDone={onDone} />
          {table.entity === "accounts" && mapped.rows.some((r) => r.balance) && (
            <OpeningBalances rows={mapped.rows} orgId={orgId} onDone={onDone} />
          )}
        </>
      )}
    </section>
  );
}

const entityLabel = (e: Entity) =>
  ({ accounts: "chart of accounts", customers: "customers", vendors: "suppliers", items: "items", invoices: "invoices", bills: "bills" })[e];

const ENTITY_OPTIONS: { value: Entity; label: string }[] = [
  { value: "accounts", label: "Chart of accounts" },
  { value: "customers", label: "Customers" },
  { value: "vendors", label: "Suppliers" },
  { value: "items", label: "Items" },
  { value: "invoices", label: "Invoices" },
  { value: "bills", label: "Bills" },
];

function MigrationImport({ table, mapped, orgId, onDone }: { table: Table; mapped: Mapped; orgId: number; onDone: () => Promise<void> }) {
  const [lookups, setLookups] = useState<LookupMaps | null>(null);
  const [failed, setFailed] = useState(false);
  const key = `${orgId}-${table.entity}`;
  useEffect(() => {
    let live = true;
    setLookups(null); setFailed(false);
    loadLookups(orgId, true).then((l) => { if (live) setLookups(l); }, () => { if (live) setFailed(true); });
    return () => { live = false; };
  }, [orgId]);
  if (failed) return <p role="alert" className="form-error">Lists for import could not be loaded. Reload the page to try again.</p>;
  if (!lookups) return <p className="muted">Loading import lists…</p>;
  const spec = importSpecs(lookups)[importKind(table.entity)];
  if (!spec) return <p role="alert" className="form-error">This record type has no importer yet.</p>;
  const csv = [spec.headers, ...mapped.rows.map((r) => spec.headers.map((h) => r[h] ?? ""))];
  return (
    <>
      <p className="section-help">Posting as {spec.label}. The importer re-checks every row against this company: unknown customers, accounts or bills are refused with a reason.</p>
      <RecordImport key={key} spec={spec} orgId={orgId} onDone={onDone} rows={csv} />
    </>
  );
}

/** Opening balances post as one conversion journal, not one request per row: the server
 *  balances the whole set and books any difference to Opening Balance Equity. */
function OpeningBalances({ rows, orgId, onDone }: { rows: Record<string, string>[]; orgId: number; onDone: () => Promise<void> }) {
  const [date, setDate] = useState("");
  const [busy, setBusy] = useState(false);
  const [log, setLog] = useState("");
  const withBalance = rows.filter((r) => r.balance);
  if (withBalance.length === 0) return null;
  async function post() {
    setBusy(true); setLog("");
    try {
      const accts = await api<{ accounts: { id: number; code: string }[] }>(`/v1/accounts?org_id=${orgId}`);
      const byCode = new Map(accts.accounts.map((a) => [a.code.trim(), a.id]));
      const missing = withBalance.filter((r) => !byCode.has(r.code.trim())).map((r) => r.code);
      if (missing.length) { setLog(`Import the chart of accounts first. Not found: ${missing.join(", ")}.`); return; }
      const lines = withBalance.map((r) => {
        // Vendor balances are debit-positive; a negative amount is a credit.
        const cents = Math.round(Number(r.balance) * 100);
        return cents >= 0
          ? { account_id: byCode.get(r.code.trim())!, description: "Opening balance", debit_cents: cents, credit_cents: 0 }
          : { account_id: byCode.get(r.code.trim())!, description: "Opening balance", debit_cents: 0, credit_cents: -cents };
      }).filter((l) => l.debit_cents > 0 || l.credit_cents > 0);
      await api("/v1/conversion-balances", { method: "POST", body: JSON.stringify({ org_id: orgId, conversion_date: date, comparative_date: "", comparative_lines: [], lines }) });
      setLog(`${lines.length} opening balances posted as of ${date}.`);
      await onDone();
    } catch (e) {
      const x = e as ApiError;
      setLog(messageFor(x.error, x.status));
    } finally { setBusy(false); }
  }
  return (
    <section className="record-form" aria-label="Opening balances">
      <h2>Opening balances</h2>
      <p className="section-help">{withBalance.length} accounts in this file carry a balance. Posting them writes one conversion journal dated the day you started using Tidal; any difference goes to Opening Balance Equity. This can only be posted once.</p>
      <label htmlFor="conversion-date">Conversion date</label>
      <input id="conversion-date" type="date" value={date} max={todayISO()} onChange={(e) => setDate(e.target.value)} />
      <div className="form-actions">
        <button type="button" disabled={busy || !date} onClick={() => void post()}>{busy ? "Posting…" : "Post opening balances"}</button>
      </div>
      {log && <p className="notice">{log}</p>}
    </section>
  );
}
