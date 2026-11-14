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

// Field names mirror taxrules.CITInput JSON tags (camelCase). Amounts are integer cents on the wire.
type CITOut = { worksheet_only: true; result: { chargeableIncome: number; netTaxPayable: number; formType: string; warnings: string[] | null; lines: { cs: string; label: string; value: number }[] } };

export default function CitWorksheet() {
  const [ya, setYa] = useState("");
  const [f, setF] = useState<Record<string, string>>({ revenue: "", netProfitBeforeTax: "", rrDeduction: "", eisDeductions: "", furtherDeductions: "", balancingCharge: "", unutilisedCaBf: "", currentYearCa: "", balancingAllowance: "", unutilisedLossesBf: "", netRentalIncome: "", interestIncome: "", otherTaxableIncome: "", unutilisedDonationsBf: "", ipcDonations: "", overseasDonations: "" });
  const [separateSource, setSeparateSource] = useState<TaxItem[]>([]);
  const [nonTaxable, setNonTaxable] = useState<TaxItem[]>([]);
  const [nonDeductible, setNonDeductible] = useState<TaxItem[]>([]);
  const [flags, setFlags] = useState({ startUpExemption: false, localEmployee: false, onlyIncomeAt17: false, claimsCarryBack: false, claimsGroupRelief: false, claimsInvestmentAllowance: false, claimsForeignTaxCredit: false });
  const [out, setOut] = useState<CITOut | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const money = ["revenue", "netProfitBeforeTax", "rrDeduction", "eisDeductions", "furtherDeductions", "balancingCharge", "unutilisedCaBf", "currentYearCa", "balancingAllowance", "unutilisedLossesBf", "netRentalIncome", "interestIncome", "otherTaxableIncome", "unutilisedDonationsBf", "ipcDonations", "overseasDonations"] as const;

  async function run(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    setOut(null);
    const yaNum = Number(ya);
    if (!Number.isInteger(yaNum) || yaNum < 2020) { setError("Enter the year of assessment, for example 2026."); return; }
    const body: Record<string, number | boolean | unknown[]> = { ya: yaNum, ...flags };
    for (const k of money) {
      const c = (k === "netProfitBeforeTax" || k === "netRentalIncome" ? parseSignedCents : parseCents)(f[k] || "0");
      if (c === null) { setError("Amounts must be numbers, for example 1200.50."); return; }
      body[k] = c;
    }
    try {
      body.separateSourceIncome = parseTaxItems(separateSource);
      body.nonTaxableIncome = parseTaxItems(nonTaxable);
      body.nonDeductible = parseTaxItems(nonDeductible);
    } catch (e) { setError((e as Error).message); return; }
    setBusy(true);
    try {
      setOut(await api<CITOut>("/v1/tax/cit", { method: "POST", body: JSON.stringify(body) }));
    } catch (err) {
      const e2 = err as ApiError;
      setError(messageFor(e2.error, e2.status));
    } finally {
      setBusy(false);
    }
  }

  const set = (k: string) => (v: string) => setF({ ...f, [k]: v });
  return (
    <>
      <PageHeader title="Company income tax worksheet (C-S)" description="Work out chargeable income and tax for one year of assessment." />
      <PreviewNotice>
        Worksheet only. Not a filed return and not certified. Tidal does not submit to IRAS. Form C and complex cases need professional review.
      </PreviewNotice>
      <form className="record-form" onSubmit={run}>
        <fieldset disabled={busy}><legend>Company tax figures</legend>
        <div className="inline-form">
          <div><label htmlFor="ya">Year of assessment</label><input id="ya" inputMode="numeric" value={ya} onChange={(e) => setYa(e.target.value.replace(/\D/g, ""))} /></div>
        </div>
        <h2>Profit and loss</h2>
        <div className="setup-grid">
          <MoneyField label="Revenue (SGD)" value={f.revenue} onChange={set("revenue")} />
          <MoneyField label="Net profit before tax (SGD, negative for loss)" value={f.netProfitBeforeTax} onChange={set("netProfitBeforeTax")} help="Use a minus sign for a loss." />
          <MoneyField label="Net rental income (SGD)" value={f.netRentalIncome} onChange={set("netRentalIncome")} />
          <MoneyField label="Interest income (SGD)" value={f.interestIncome} onChange={set("interestIncome")} />
          <MoneyField label="Other taxable income (SGD)" value={f.otherTaxableIncome} onChange={set("otherTaxableIncome")} />
        </div>
        <TaxItems title="Income taxed separately" help="Income already included in profit that must be removed from trade profit. Enter its taxable amount once under rental, interest or other income above." items={separateSource} onChange={setSeparateSource} />
        <TaxItems title="Non-taxable income" help="Income included in profit but not taxable. Enter separate amounts with supporting references." items={nonTaxable} onChange={setNonTaxable} />
        <TaxItems title="Non-deductible expenses" help="Expenses included in profit that cannot be claimed for tax. Keep separate descriptions, reasons and references." items={nonDeductible} onChange={setNonDeductible} />
        <h2>Deductions and reliefs</h2>
        <div className="setup-grid">
          <MoneyField label="Section 14N deduction (SGD)" value={f.rrDeduction} onChange={set("rrDeduction")} />
          <MoneyField label="EIS enhanced deductions (SGD)" value={f.eisDeductions} onChange={set("eisDeductions")} />
          <MoneyField label="Further deductions (SGD)" value={f.furtherDeductions} onChange={set("furtherDeductions")} />
          <MoneyField label="Unutilised donations brought forward (SGD)" value={f.unutilisedDonationsBf} onChange={set("unutilisedDonationsBf")} />
          <MoneyField label="Donations to approved IPCs, actual (SGD)" value={f.ipcDonations} onChange={set("ipcDonations")} />
          <MoneyField label="Qualifying overseas donations (SGD)" value={f.overseasDonations} onChange={set("overseasDonations")} />
        </div>
        <h2>Capital allowances and losses</h2>
        <div className="setup-grid">
          <MoneyField label="Balancing charge (SGD)" value={f.balancingCharge} onChange={set("balancingCharge")} />
          <MoneyField label="Unutilised capital allowances brought forward (SGD)" value={f.unutilisedCaBf} onChange={set("unutilisedCaBf")} />
          <MoneyField label="Current-year capital allowances (SGD)" value={f.currentYearCa} onChange={set("currentYearCa")} />
          <MoneyField label="Balancing allowance (SGD)" value={f.balancingAllowance} onChange={set("balancingAllowance")} />
          <MoneyField label="Unutilised losses brought forward (SGD)" value={f.unutilisedLossesBf} onChange={set("unutilisedLossesBf")} />
        </div>
        <h2>Declarations</h2>
        <CheckField label="Start-up exemption applies" checked={flags.startUpExemption} onChange={(v) => setFlags({ ...flags, startUpExemption: v })} />
        <CheckField label="Local employee (for the rebate)" checked={flags.localEmployee} onChange={(v) => setFlags({ ...flags, localEmployee: v })} />
        <CheckField label="Only income taxed at 17%" checked={flags.onlyIncomeAt17} onChange={(v) => setFlags({ ...flags, onlyIncomeAt17: v })} />
        <CheckField label="Claims carry-back of losses" checked={flags.claimsCarryBack} onChange={(v) => setFlags({ ...flags, claimsCarryBack: v })} />
        <CheckField label="Claims group relief" checked={flags.claimsGroupRelief} onChange={(v) => setFlags({ ...flags, claimsGroupRelief: v })} />
        <CheckField label="Claims investment allowance" checked={flags.claimsInvestmentAllowance} onChange={(v) => setFlags({ ...flags, claimsInvestmentAllowance: v })} />
        <CheckField label="Claims foreign tax credit" checked={flags.claimsForeignTaxCredit} onChange={(v) => setFlags({ ...flags, claimsForeignTaxCredit: v })} />

        </fieldset>
        {error && <p role="alert" className="form-error">{error}</p>}
        <button type="submit" disabled={busy}>{busy ? "Working…" : "Calculate worksheet"}</button>
      </form>

      {out && (
        <section className="worksheet" aria-labelledby="cit-out">
          <h2 id="cit-out">Result (worksheet only)</h2>
          <p>Chargeable income: <strong>{formatCents(out.result.chargeableIncome)}</strong>. Net tax payable: <strong>{formatCents(out.result.netTaxPayable)}</strong>. Form type suggested: {out.result.formType}.</p>
          <div className="table-scroll"><table className="doc-table">
            <caption className="sr-only">Form lines</caption>
            <thead><tr><th scope="col">Form C-S line</th><th scope="col">Label</th><th scope="col" className="num">Value</th></tr></thead>
            <tbody>{out.result.lines.map((l) => <tr key={l.cs + l.label}><td>{l.cs}</td><td>{l.label}</td><td className="num">{formatCents(l.value)}</td></tr>)}</tbody>
          </table></div>
          {!!out.result.warnings?.length && <ul className="warnings">{out.result.warnings.map((w, i) => <li key={i}>{w}</li>)}</ul>}
        </section>
      )}
    </>
  );
}
