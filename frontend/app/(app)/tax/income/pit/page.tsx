"use client";
import { useState } from "react";
import PageHeader from "@/components/PageHeader";
import PreviewNotice from "@/components/PreviewNotice";
import MoneyField from "@/components/MoneyField";
import CheckField from "@/components/CheckField";
import { api, type ApiError } from "@/lib/api";
import TaxItems, { type TaxItem } from "@/components/TaxItems";
import { parseTaxItems } from "@/lib/tax-items";
import { parseCents, parseSignedCents, formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";

// Field names mirror taxrules.PITInput JSON tags (camelCase). Amounts are integer cents on the wire.
type PITOut = { worksheet_only: true; result: { chargeableIncome: number; netTax: number; formType: string; statementLines: number; filingDue: string; warnings: string[] | null } };

const MONEY = ["revenue", "grossProfit", "allowableExpenses", "partnershipShare", "capitalAllowances", "balancingCharge", "lossesBf", "capitalAllowancesBf", "interestExcluded", "employmentIncome", "netRentalIncome", "otherIncome", "ipcDonations"] as const;
const LABELS: Record<(typeof MONEY)[number], string> = {
  revenue: "Revenue (SGD)",
  grossProfit: "Gross profit (SGD)",
  allowableExpenses: "Allowable expenses (SGD)",
  partnershipShare: "Partnership share of profit (SGD, partners only)",
  capitalAllowances: "Capital allowances this year (SGD)",
  balancingCharge: "Balancing charge (SGD)",
  lossesBf: "Losses brought forward (SGD)",
  capitalAllowancesBf: "Capital allowances brought forward (SGD)",
  interestExcluded: "Business bank interest moved out of trade income (SGD)",
  employmentIncome: "Employment income (SGD)",
  netRentalIncome: "Net rental income (SGD)",
  otherIncome: "Other income (SGD)",
  ipcDonations: "Donations to approved IPCs, actual (SGD)",
};

const RELIEFS = [
  ["spouse", "Spouse relief"], ["qcr", "Qualifying child relief"], ["wmcr", "Working mother's child relief"], ["parent", "Parent relief"], ["nsman", "NSman relief"],
  ["cpf_self_employed", "Self-employed CPF (legacy combined amount)"], ["cpf_medisave_compulsory", "Compulsory MediSave"], ["cpf_voluntary", "Voluntary CPF"], ["life_insurance", "Life insurance"],
  ["srs", "Supplementary Retirement Scheme (SRS)"], ["cpf_topup", "CPF cash top-up"], ["course_fees", "Course fees (up to YA 2025 only)"], ["other", "Other eligible relief"],
].map(([value, label]) => ({ value, label }));

export default function PitWorksheet() {
  const [ya, setYa] = useState("");
  const [age, setAge] = useState("");
  const [flags, setFlags] = useState({ resident: true, foreigner: false, partner: false });
  const [f, setF] = useState<Record<string, string>>(Object.fromEntries(MONEY.map((k) => [k, ""])));
  const [reliefs, setReliefs] = useState<TaxItem[]>([]);
  const [caIssues, setCAIssues] = useState("");
  const [out, setOut] = useState<PITOut | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function run(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setOut(null);
    const yaNum = Number(ya);
    const ageNum = Number(age);
    if (!Number.isInteger(yaNum) || yaNum < 2023) { setError("Enter a year of assessment from 2023."); return; }
    if (!Number.isInteger(ageNum) || ageNum < 0 || ageNum > 120) { setError("Enter age on 31 December of the year before, between 0 and 120."); return; }
    if (!age.trim()) { setError("Enter age on 31 December of the year before."); return; }
    const body: Record<string, number | boolean | unknown[]> = { ya: yaNum, age: ageNum, ...flags, capitalAllowanceIssues: caIssues.split("\n").map(line => line.trim()).filter(Boolean) };
    try { body.reliefs = parseTaxItems(reliefs); } catch (e) { setError((e as Error).message); return; }
    for (const k of MONEY) {
      const c = (["grossProfit", "partnershipShare", "netRentalIncome"].includes(k) ? parseSignedCents : parseCents)(f[k] || "0");
      if (c === null) { setError("Amounts must be numbers, for example 1200.50."); return; }
      body[k] = c;
    }
    setBusy(true);
    try {
      setOut(await api<PITOut>("/v1/tax/pit", { method: "POST", body: JSON.stringify(body) }));
    } catch (err) {
      const e2 = err as ApiError;
      setError(messageFor(e2.error, e2.status));
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <PageHeader title="Individual income tax worksheet (Form B)" description="Sole proprietor or partner: four-line business statement and personal figures." />
      <PreviewNotice>
        Worksheet only. Not a filed return and not certified. Tidal does not submit to IRAS, and this
        year&apos;s rule table has not been cross-checked against an IRAS worked example. Check every figure before you file.
      </PreviewNotice>
      <form className="record-form" onSubmit={run}>
        <fieldset disabled={busy}><legend>Personal and business figures</legend>
        <div className="inline-form">
          <div><label htmlFor="pya">Year of assessment</label><input id="pya" inputMode="numeric" value={ya} onChange={(e) => setYa(e.target.value.replace(/\D/g, ""))} /></div>
          <div><label htmlFor="page">Age on 31 December of the year before</label><input id="page" inputMode="numeric" value={age} onChange={(e) => setAge(e.target.value.replace(/\D/g, ""))} /></div>
        </div>
        <CheckField label="Resident in Singapore" checked={flags.resident} onChange={(v) => setFlags({ ...flags, resident: v })} />
        <CheckField label="Foreigner (non-citizen, non-PR)" checked={flags.foreigner} onChange={(v) => setFlags({ ...flags, foreigner: v })} />
        <CheckField label="Partner in a partnership" checked={flags.partner} onChange={(v) => setFlags({ ...flags, partner: v })} />
        <h2>Business statement</h2>
        <div className="setup-grid">
          {(["revenue", "grossProfit", "allowableExpenses", "partnershipShare", "capitalAllowances", "balancingCharge", "lossesBf", "capitalAllowancesBf", "interestExcluded"] as const).map((k) => (
            <MoneyField key={k} label={LABELS[k]} value={f[k]} onChange={(v) => setF({ ...f, [k]: v })} />
          ))}
        </div>
        <h2>Personal income and donations</h2>
        <div className="setup-grid">
          {(["employmentIncome", "netRentalIncome", "otherIncome", "ipcDonations"] as const).map((k) => (
            <MoneyField key={k} label={LABELS[k]} value={f[k]} onChange={(v) => setF({ ...f, [k]: v })} />
          ))}
        </div>
        <p className="section-help">Use a minus sign for a business loss, partnership loss or rental deficit. Enter only reliefs whose eligibility you have checked; earned income relief is calculated automatically.</p>
        <TaxItems title="Personal relief claims" help="Enter eligible amounts in SGD. The calculator applies supported caps and warnings; it does not establish eligibility. For YA 2026 onward, enter compulsory MediSave and voluntary CPF separately." items={reliefs} onChange={setReliefs} options={RELIEFS} />
        <fieldset><legend>Capital allowance review notes</legend><label htmlFor="pit-ca-issues">Outstanding issues <span className="field-optional">Optional, one per line</span></label><textarea id="pit-ca-issues" rows={3} maxLength={10000} value={caIssues} onChange={e => setCAIssues(e.target.value)} /><p className="section-help">These appear as warnings, not deductions.</p></fieldset>
        </fieldset>
        {error && <p role="alert" className="form-error">{error}</p>}
        <button type="submit" disabled={busy}>{busy ? "Working…" : "Calculate worksheet"}</button>
      </form>

      {out && (
        <section className="worksheet" aria-labelledby="pit-out">
          <h2 id="pit-out">Result (worksheet only)</h2>
          <p>Chargeable income: <strong>{formatCents(out.result.chargeableIncome)}</strong>. Net tax: <strong>{formatCents(out.result.netTax)}</strong>.</p>
          <p className="muted">Business statement lines needed: {out.result.statementLines}. Filing due: {out.result.filingDue.slice(0, 10)}. Form: {out.result.formType}.</p>
          {!!out.result.warnings?.length && <ul className="warnings">{out.result.warnings.map((w, i) => <li key={i}>{w}</li>)}</ul>}
        </section>
      )}
    </>
  );
}
