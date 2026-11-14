"use client";
import { useEffect, useRef, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import PreviewNotice from "@/components/PreviewNotice";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { parseCents, formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Asset = { id: number; name: string; acquired_on: string; cost_cents: number; useful_life_months: number; depreciated_months: number; net_book_cents: number; depreciation_method: string; averaging_rule: string; residual_value_cents: number; last_depreciation?: string };

// Straight-line depreciation, one month at a time. Each run posts only months not yet posted.
export default function AssetsPage() {
  const { selected } = useOrg();
  return <AssetsWorkspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function AssetsWorkspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const live = useRef(true);
  const [rows, setRows] = useState<Asset[] | null>(null);
  const [name, setName] = useState("");
  const [acquired, setAcquired] = useState("");
  const [cost, setCost] = useState("");
  const [life, setLife] = useState("36");
  const [method, setMethod] = useState("straight_line");
  const [averaging, setAveraging] = useState("full_month");
  const [residual, setResidual] = useState("0.00");
  const [manualAmounts, setManualAmounts] = useState<Record<number, string>>({});
  const [csvText, setCsvText] = useState("");
  const [month, setMonth] = useState("");
  const [accounts, setAccounts] = useState<{ id: number; code: string; name: string; type: string }[]>([]);
  const [disposeId, setDisposeId] = useState("");
  const [disposedOn, setDisposedOn] = useState("");
  const [proceeds, setProceeds] = useState("");
  const [proceedsAccount, setProceedsAccount] = useState("");
  const [revalueId, setRevalueId] = useState("");
  const [revaluedOn, setRevaluedOn] = useState("");
  const [revalueAmount, setRevalueAmount] = useState("");
  const [revalueNote, setRevalueNote] = useState("");
  const [revaluations, setRevaluations] = useState<{ id: number; revalued_on: string; amount_cents: number; note: string; reversed: boolean }[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  async function load() {
    if (orgId == null) return;
    const r = await api<{ assets: Asset[] }>(`/v1/assets?org_id=${orgId}`);
    if (live.current) setRows(r.assets);
  }
  useEffect(() => {
    live.current = true;
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    if (orgId != null) {
      api<{ accounts: { id: number; code: string; name: string; type: string }[] }>(`/v1/accounts?org_id=${orgId}`)
        .then((r) => { if (!live.current) return; const assets = r.accounts.filter((a) => a.type === "asset"); setAccounts(assets); setProceedsAccount((v) => v || (assets[0] ? String(assets[0].id) : "")); })
        .catch(() => { if (live.current) setError("Could not load accounts."); });
    }
    return () => { live.current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function run(work: () => Promise<void>) {
    if (busy || !canWrite || orgId == null) return;
    setBusy(true); setError(""); setNotice("");
    try { await work(); } catch (e) { if (live.current) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } }
    finally { if (live.current) setBusy(false); }
  }

  async function add(e: FormEvent) {
    e.preventDefault();
    const cents = parseCents(cost);
    const months = Number(life);
    if (!name.trim() || !acquired || cents === null || cents < 1 || !Number.isInteger(months) || months < 1 || months > 600) {
      setError("Enter a name, acquisition date, cost above zero and a life of 1 to 600 months.");
      return;
    }
    await run(async () => {
      await api("/v1/assets", { method: "POST", body: JSON.stringify({ org_id: orgId, name: name.trim(), acquired_on: acquired, cost_cents: cents, useful_life_months: months, depreciation_method: method, averaging_rule: averaging, residual_value_cents: parseCents(residual) ?? 0 }) });
      if (!live.current) return;
      setName(""); setAcquired(""); setCost("");
      setNotice("Asset added. Record its purchase separately as a journal if not already done.");
      await load();
    });
  }

  async function dispose(e: FormEvent) {
    e.preventDefault();
    const cents = parseCents(proceeds || "0");
    if (!disposeId || !disposedOn || cents == null || cents < 0 || !proceedsAccount) {
      setError("Choose the asset, the disposal date, the proceeds and the account the money went to.");
      return;
    }
    await run(async () => {
      await api(`/v1/assets/${disposeId}/dispose`, { method: "POST", body: JSON.stringify({ org_id: orgId, disposed_on: disposedOn, proceeds_cents: cents, proceeds_account_id: Number(proceedsAccount) }) });
      if (!live.current) return;
      setNotice("Asset disposed. The gain or loss was posted and depreciation has stopped.");
      setDisposeId(""); setDisposedOn(""); setProceeds("");
      await load();
    });
  }

  async function loadRevaluations(id: string) {
    if (orgId == null || !id) { setRevaluations([]); return; }
    try { const r = await api<{ revaluations: typeof revaluations }>(`/v1/assets/${id}/revaluations?org_id=${orgId}`); setRevaluations(r.revaluations); }
    catch { setRevaluations([]); }
  }
  async function revalue(e: FormEvent) {
    e.preventDefault();
    const cents = parseCents(revalueAmount.replace("-", ""));
    const negative = revalueAmount.trim().startsWith("-");
    if (!revalueId || !revaluedOn || cents == null || cents === 0) { setError("Choose the asset, the date and a non-zero amount. Use a minus sign to write the asset down."); return; }
    await run(async () => {
      await api(`/v1/assets/${revalueId}/revalue`, { method: "POST", body: JSON.stringify({ org_id: orgId, revalued_on: revaluedOn, amount_cents: negative ? -cents : cents, note: revalueNote }) });
      setNotice("Revaluation posted to the revaluation reserve.");
      setRevalueAmount(""); setRevalueNote("");
      await load(); await loadRevaluations(revalueId);
    });
  }
  async function rollback(revaluationId: number) {
    await run(async () => {
      await api(`/v1/assets/${revalueId}/revaluations/${revaluationId}/rollback`, { method: "POST", body: JSON.stringify({ org_id: orgId, reversed_on: new Date().toISOString().slice(0, 10) }) });
      setNotice("Revaluation rolled back with a reversal entry.");
      await load(); await loadRevaluations(revalueId);
    });
  }

  async function depreciate(e: FormEvent) {
    e.preventDefault();
    if (!month) { setError("Choose the month to depreciate."); return; }
    await run(async () => {
      const manual = (rows ?? []).filter(a => a.depreciation_method === "manual" && manualAmounts[a.id]).map(a => ({ asset_id: a.id, amount_cents: parseCents(manualAmounts[a.id]) ?? 0 }));
      const r = await api<{ assets_depreciated: number[] }>("/v1/assets/depreciation/run", { method: "POST", body: JSON.stringify({ org_id: orgId, month, manual_amounts: manual }) });
      if (!live.current) return;
      setNotice(r.assets_depreciated.length === 0 ? "Nothing to depreciate for that month (already posted or fully depreciated)." : `Depreciation posted for ${r.assets_depreciated.length} asset(s).`);
      await load();
    });
  }

  return (
    <>
      <PageHeader title="Fixed assets" description="Equipment and other long-life assets, written down over their useful life." />
      <PreviewNotice>Depreciation supports straight-line, diminishing-value and manual methods. A revaluation posts to the revaluation reserve; a rollback reverses it through the normal reversal log.</PreviewNotice>
      {!selected && <p>Choose a company first.</p>}
      {error && <p role="alert" className="form-error">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      {selected && canWrite && (
        <>
          <form className="record-form" onSubmit={add}>
            <h2>New asset</h2>
            <fieldset disabled={busy}>
              <legend>Asset</legend>
              <div className="form-grid">
                <div><label htmlFor="fa-name">Name</label><input id="fa-name" required maxLength={200} value={name} onChange={(e) => setName(e.target.value)} /></div>
                <div><label htmlFor="fa-acq">Date acquired</label><DateInput id="fa-acq" required value={acquired} onChange={(e) => setAcquired(e.target.value)}/></div>
                <div><label htmlFor="fa-cost">Cost (SGD)</label><input id="fa-cost" required inputMode="decimal" value={cost} onChange={(e) => setCost(e.target.value)} /></div>
                <div><label htmlFor="fa-life">Useful life (months)</label><input id="fa-life" inputMode="numeric" required value={life} onChange={(e) => setLife(e.target.value.replace(/\D/g, ""))} /></div>
                <div><label htmlFor="fa-method">Depreciation method</label><select id="fa-method" value={method} onChange={e => setMethod(e.target.value)}><option value="straight_line">Straight line</option><option value="diminishing_value">Diminishing value</option><option value="manual">Manual</option></select></div>
                <div><label htmlFor="fa-averaging">Averaging rule</label><select id="fa-averaging" value={averaging} onChange={e => setAveraging(e.target.value)}><option value="full_month">Full month</option><option value="half_month">Half month</option></select></div>
                <div><label htmlFor="fa-residual">Residual value (SGD)</label><input id="fa-residual" inputMode="decimal" value={residual} onChange={e => setResidual(e.target.value)} /></div>
              </div>
              <button disabled={busy}>Add asset</button>
            </fieldset>
          </form>
          <form className="record-form" onSubmit={async e => { e.preventDefault(); if (!csvText.trim()) return; await run(async () => { await api("/v1/assets/import", { method: "POST", body: JSON.stringify({ org_id: orgId, csv: csvText }) }); setCsvText(""); setNotice("Asset registry imported."); await load(); }); }}><h2>Import asset register (CSV)</h2><label htmlFor="fa-csv">CSV columns: name, acquired_on, cost_cents, useful_life_months; optional depreciation_method, averaging_rule, residual_value_cents</label><textarea id="fa-csv" value={csvText} onChange={e => setCsvText(e.target.value)} rows={5} /><button disabled={busy || !csvText.trim()}>Import CSV</button></form>
          <form className="record-form" onSubmit={depreciate}>
            <h2>Post monthly depreciation</h2>
            <fieldset disabled={busy}>
              <label htmlFor="fa-month">Month</label>
              <input id="fa-month" type="month" required value={month} onChange={(e) => setMonth(e.target.value)} />
              <button disabled={busy}>Post depreciation for this month</button>
            </fieldset>
          </form>
          <form className="record-form" onSubmit={revalue}>
            <h2>Revalue an asset</h2>
            <fieldset disabled={busy}>
              <legend>Revaluation</legend>
              <p className="muted small">A positive amount writes the asset up, a negative amount writes it down. The other side goes to the revaluation reserve, and a rollback posts a reversal rather than editing history.</p>
              <div className="form-grid">
                <div><label htmlFor="fa-revalue">Asset</label><select id="fa-revalue" required value={revalueId} onChange={(e) => { setRevalueId(e.target.value); void loadRevaluations(e.target.value); }}><option value="">Choose an asset</option>{(rows ?? []).map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}</select></div>
                <div><label htmlFor="fa-revalued-on">Revaluation date</label><DateInput id="fa-revalued-on" required value={revaluedOn} onChange={(e) => setRevaluedOn(e.target.value)} /></div>
                <div><label htmlFor="fa-revalue-amount">Amount (SGD)</label><input id="fa-revalue-amount" required inputMode="decimal" value={revalueAmount} onChange={(e) => setRevalueAmount(e.target.value)} placeholder="2500.00 or -2500.00" /></div>
                <div><label htmlFor="fa-revalue-note">Note</label><input id="fa-revalue-note" maxLength={500} value={revalueNote} onChange={(e) => setRevalueNote(e.target.value)} /></div>
              </div>
              <button disabled={busy || !revalueId}>Post revaluation</button>
              {revaluations.length > 0 && <div className="table-scroll"><table className="doc-table"><caption>Revaluations for the chosen asset</caption><thead><tr><th>Date</th><th className="num">Amount</th><th>Note</th><th>Action</th></tr></thead><tbody>
                {revaluations.map(r => <tr key={r.id}><td>{r.revalued_on}</td><td className="num">{formatCents(r.amount_cents)}</td><td>{r.note || "—"}</td><td>{r.reversed ? "Rolled back" : <button type="button" className="button-secondary" disabled={busy} onClick={() => void rollback(r.id)}>Roll back</button>}</td></tr>)}
              </tbody></table></div>}
            </fieldset>
          </form>
          <form className="record-form" onSubmit={dispose}>
            <h2>Dispose of an asset</h2>
            <fieldset disabled={busy}>
              <legend>Disposal</legend>
              <p className="muted small">Sells or scraps one asset. Posts the opposite of cost and accumulated depreciation, plus the gain or loss, and stops further depreciation. The register keeps the asset as disposed.</p>
              <div className="form-grid">
                <div><label htmlFor="fa-dispose">Asset</label><select id="fa-dispose" required value={disposeId} onChange={(e) => setDisposeId(e.target.value)}><option value="">Choose an asset</option>{(rows ?? []).map((a) => <option key={a.id} value={a.id}>{a.name}</option>)}</select></div>
                <div><label htmlFor="fa-disposed-on">Disposal date</label><DateInput id="fa-disposed-on" required value={disposedOn} onChange={(e) => setDisposedOn(e.target.value)} /></div>
                <div><label htmlFor="fa-proceeds">Proceeds (SGD)</label><input id="fa-proceeds" inputMode="decimal" value={proceeds} onChange={(e) => setProceeds(e.target.value)} placeholder="0.00" /></div>
                <div><label htmlFor="fa-proceeds-account">Money went to</label><select id="fa-proceeds-account" required value={proceedsAccount} onChange={(e) => setProceedsAccount(e.target.value)}>{accounts.map((a) => <option key={a.id} value={a.id}>{a.code} {a.name}</option>)}</select></div>
              </div>
              <button className="button-secondary" disabled={busy}>Dispose of asset</button>
            </fieldset>
          </form>
        </>
      )}
      {rows === null && !error && <p role="status">Loading assets…</p>}
      {rows && rows.length === 0 && <p>No fixed assets yet.</p>}
      {rows && rows.some(a => a.depreciation_method === "manual") && <section className="record-form"><h2>Manual depreciation amounts (SGD)</h2>{rows.filter(a => a.depreciation_method === "manual").map(a => <div key={a.id}><label htmlFor={`fa-manual-${a.id}`}>{a.name}</label><input id={`fa-manual-${a.id}`} inputMode="decimal" value={manualAmounts[a.id] ?? ""} onChange={e => setManualAmounts({ ...manualAmounts, [a.id]: e.target.value })} /></div>)}</section>}
      {rows && rows.length > 0 && (
        <div className="table-scroll"><table className="doc-table">
          <caption>Latest 200 assets</caption>
          <thead><tr><th>Asset</th><th>Acquired</th><th className="num">Cost</th><th>Life (months)</th><th>Months done</th><th>Method</th><th>Averaging rule</th><th className="num">Residual</th><th className="num">Net book value</th><th>Last depreciation</th></tr></thead>
          <tbody>{rows.map((a) => <tr key={a.id}><th scope="row">{a.name}</th><td>{a.acquired_on}</td><td className="num">{formatCents(a.cost_cents)}</td><td>{a.useful_life_months}</td><td>{a.depreciated_months}</td><td>{a.depreciation_method}</td><td>{a.averaging_rule}</td><td className="num">{formatCents(a.residual_value_cents)}</td><td className="num">{formatCents(a.net_book_cents)}</td><td>{a.last_depreciation || "None yet"}</td></tr>)}</tbody>
        </table></div>
      )}
    </>
  );
}
