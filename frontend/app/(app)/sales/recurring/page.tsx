"use client";
import { useEffect, useRef, useState } from "react";
import PageHeader from "@/components/PageHeader";
import PreviewNotice from "@/components/PreviewNotice";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { parseCents, formatCents } from "@/lib/money";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";

type Template = { id: number; name: string; customer_name: string; frequency: string; next_run: string; total_cents: number; active: boolean };

// Recurring templates create invoice DRAFTS on each due date. Nothing is posted or sent automatically.
export default function RecurringPage() {
  const { selected } = useOrg();
  return <RecurringWorkspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function RecurringWorkspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const live = useRef(true);
  const [rows, setRows] = useState<Template[] | null>(null);
  const [customers, setCustomers] = useState<{ id: number; name: string }[]>([]);
  const [name, setName] = useState("");
  const [contact, setContact] = useState("");
  const [frequency, setFrequency] = useState("monthly");
  const [start, setStart] = useState("");
  const [termDays, setTermDays] = useState("30");
  const [reference, setReference] = useState("");
  const [endDate, setEndDate] = useState("");
  const [dueDay, setDueDay] = useState("");
  type FormLine = { description: string; quantity: string; price: string; gstCode: string };
  const blankLine = (): FormLine => ({ description: "", quantity: "1", price: "", gstCode: "NONE" });
  const [formLines, setFormLines] = useState<FormLine[]>([blankLine()]);
  const [amountsAre, setAmountsAre] = useState("exclusive");
  const [runDate, setRunDate] = useState("");
  const [notice, setNotice] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function load() {
    if (orgId == null) return;
    const [t, c] = await Promise.all([
      api<{ templates: Template[] }>(`/v1/recurring?org_id=${orgId}`),
      api<{ contacts: { id: number; name: string }[] }>(`/v1/contacts?org_id=${orgId}&role=customer`),
    ]);
    if (!live.current) return;
    setRows(t.templates);
    setCustomers(c.contacts);
  }
  useEffect(() => {
    live.current = true;
    // "Make recurring" on an invoice lands here with its customer and first line filled in.
    const q = new URLSearchParams(window.location.search);
    if (/^\d+$/.test(q.get("customer") ?? "")) {
      setContact(q.get("customer")!);
      setFormLines([{ description: (q.get("description") ?? "").slice(0, 200), quantity: /^\d{1,7}$/.test(q.get("quantity") ?? "") ? q.get("quantity")! : "1", price: /^\d+\.\d{2}$/.test(q.get("price") ?? "") ? q.get("price")! : "", gstCode: q.get("gst_code") === "SR" ? "SR" : "NONE" }]);
      if (q.get("amounts_are") === "inclusive") setAmountsAre("inclusive");
    }
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function run(work: () => Promise<void>) {
    if (busy || !canWrite || orgId == null) return;
    setBusy(true); setError(""); setNotice("");
    try { await work(); } catch (e) { if (live.current) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } }
    finally { if (live.current) setBusy(false); }
  }

  async function create(e: React.FormEvent) {
    e.preventDefault();
    const parsed = formLines.map(l => ({ description: l.description.trim(), quantity: Number(l.quantity), unit_price_cents: parseCents(l.price), gst_code: l.gstCode }));
    if (!name.trim() || !contact || !start || parsed.length === 0 || parsed.length > 200 || parsed.some(l => !l.description || l.unit_price_cents === null || l.unit_price_cents < 0 || !Number.isInteger(l.quantity) || l.quantity < 1)) {
      setError("Enter a name, customer, start date, and one or more lines with a description, whole quantity and price.");
      return;
    }
    if (dueDay !== "" && (!/^\d{1,2}$/.test(dueDay) || Number(dueDay) < 1 || Number(dueDay) > 31)) {
      setError("Due day is a day of the month from 1 to 31, or blank for payment terms.");
      return;
    }
    await run(async () => {
      await api("/v1/recurring", { method: "POST", body: JSON.stringify({ org_id: orgId, name: name.trim(), contact_id: Number(contact), frequency, start_date: start, term_days: Number(termDays), ...(dueDay === "" ? {} : { due_day: Number(dueDay) }), reference: reference.trim(), end_date: endDate || "", amounts_are: amountsAre, lines: parsed }) });
      if (!live.current) return;
      setName(""); setContact(""); setStart(""); setReference(""); setDueDay(""); setFormLines([blankLine()]); setAmountsAre("exclusive");
      setNotice("Template saved. Drafts appear in Invoices when a run is due.");
      await load();
    });
  }

  async function generate(e: React.FormEvent) {
    e.preventDefault();
    if (!runDate) { setError("Choose the date to generate drafts for."); return; }
    await run(async () => {
      const r = await api<{ created_invoice_ids: number[]; skipped_template_ids?: number[] }>("/v1/recurring/run", { method: "POST", body: JSON.stringify({ org_id: orgId, as_of: runDate }) });
      if (!live.current) return;
      const skipped = r.skipped_template_ids?.length ?? 0;
      const made = r.created_invoice_ids.length === 0 ? "No drafts were created." : `${r.created_invoice_ids.length} draft invoice(s) created. Review them under Invoices before posting.`;
      setNotice(skipped ? `${made} ${skipped} template(s) were skipped because their GST setting is not allowed for this company; fix them and run again.` : r.created_invoice_ids.length === 0 ? "No templates were due on that date." : made);
      await load();
    });
  }

  return (
    <>
      <PageHeader title="Recurring invoices" description="Repeat a sale on a schedule. Each run creates a draft for you to check." />
      <PreviewNotice>Drafts only. Tidal does not post, send or collect payment for recurring invoices.</PreviewNotice>
      {!selected && <p>Choose a company first.</p>}
      {error && <p role="alert" className="form-error">{error}</p>}
      {notice && <p role="status">{notice}</p>}
      {selected && canWrite && (
        <>
          <form className="record-form" onSubmit={create}>
            <h2>New template</h2>
            <fieldset disabled={busy}>
              <legend>Schedule</legend>
              <div className="form-grid">
                <div><label htmlFor="rc-name">Template name</label><input id="rc-name" required maxLength={200} value={name} onChange={(e) => setName(e.target.value)} /></div>
                <div><label htmlFor="rc-contact">Customer</label><select id="rc-contact" required value={contact} onChange={(e) => setContact(e.target.value)}><option value="">Choose…</option>{customers.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}</select></div>
                <div><label htmlFor="rc-freq">How often</label><select id="rc-freq" value={frequency} onChange={(e) => setFrequency(e.target.value)}><option value="monthly">Monthly</option><option value="quarterly">Quarterly</option><option value="yearly">Yearly</option></select></div>
                <div><label htmlFor="rc-start">First run date</label><DateInput id="rc-start" required value={start} onChange={(e) => setStart(e.target.value)}/></div>
                <div><label htmlFor="rc-reference">Reference <span className="field-optional">Optional</span></label><input id="rc-reference" maxLength={100} value={reference} onChange={e => setReference(e.target.value)} /></div>
                <div><label htmlFor="rc-end">End date <span className="field-optional">Optional</span></label><DateInput id="rc-end" value={endDate} onChange={(e) => setEndDate(e.target.value)}/></div>
                <div><label htmlFor="rc-term">Payment term (days)</label><input id="rc-term" inputMode="numeric" value={termDays} onChange={(e) => setTermDays(e.target.value.replace(/\D/g, ""))} /></div>
                <div><label htmlFor="rc-due-day">Due day <span className="field-optional">Optional</span></label><input id="rc-due-day" inputMode="numeric" placeholder="Blank = term days" value={dueDay} onChange={(e) => setDueDay(e.target.value.replace(/\D/g, "").slice(0, 2))} /><p className="section-help">Day of the month the invoice is due, in the month after the invoice month. Blank keeps the payment term above.</p></div>
              </div>
            </fieldset>
            <fieldset disabled={busy}>
              <legend>What is billed each time</legend>
              {formLines.map((l, i) => <fieldset key={i}><legend>Line {i + 1}</legend><div className="form-grid">
                <div><label htmlFor={`rc-desc-${i}`}>Description</label><input id={`rc-desc-${i}`} required maxLength={200} value={l.description} onChange={(e) => setFormLines(formLines.map((v, j) => i === j ? { ...v, description: e.target.value } : v))} /></div>
                <div><label htmlFor={`rc-qty-${i}`}>Quantity</label><input id={`rc-qty-${i}`} inputMode="numeric" required value={l.quantity} onChange={(e) => setFormLines(formLines.map((v, j) => i === j ? { ...v, quantity: e.target.value.replace(/\D/g, "") } : v))} /></div>
                <div><label htmlFor={`rc-price-${i}`}>Unit price (SGD)</label><input id={`rc-price-${i}`} inputMode="decimal" required value={l.price} onChange={(e) => setFormLines(formLines.map((v, j) => i === j ? { ...v, price: e.target.value } : v))} /></div>
                <div><label htmlFor={`rc-tax-${i}`}>Tax</label><select id={`rc-tax-${i}`} value={l.gstCode} onChange={e => setFormLines(formLines.map((v, j) => i === j ? { ...v, gstCode: e.target.value } : v))}><option value="NONE">No GST</option><option value="SR">Standard-rate GST</option></select></div>
              </div>{formLines.length > 1 && <button type="button" className="button-secondary" onClick={() => setFormLines(formLines.filter((_, j) => j !== i))}>Remove line {i + 1}</button>}</fieldset>)}
              <button type="button" className="button-secondary" disabled={formLines.length >= 200} onClick={() => setFormLines([...formLines, blankLine()])}>Add line</button>
              <div className="form-grid">
                <div><label htmlFor="rc-amounts">Amounts are</label><select id="rc-amounts" value={amountsAre} onChange={e => setAmountsAre(e.target.value)}><option value="exclusive">Tax exclusive</option><option value="inclusive">Tax inclusive</option></select></div>
              </div>
              <p className="section-help">Standard-rate GST requires a GST-registered company. Each draft uses the rate on its run date. Prices are entered before GST unless tax inclusive is selected.</p>
              <button disabled={busy}>{busy ? "Saving…" : "Save template"}</button>
            </fieldset>
          </form>
          <form className="record-form" onSubmit={generate}>
            <h2>Generate due drafts</h2>
            <fieldset disabled={busy}>
              <label htmlFor="rc-run">Generate drafts due on or before</label>
              <DateInput id="rc-run" required value={runDate} onChange={(e) => setRunDate(e.target.value)}/>
              <button disabled={busy}>Generate drafts</button>
            </fieldset>
          </form>
        </>
      )}
      {rows === null && !error && <p role="status">Loading templates…</p>}
      {rows && rows.length === 0 && <p>No recurring templates yet.</p>}
      {rows && rows.length > 0 && (
        <div className="table-scroll"><table className="doc-table">
          <caption>Latest 200 templates</caption>
          <thead><tr><th>Template</th><th>Customer</th><th>Repeats</th><th>Next run</th><th className="num">Typed amount before GST split</th></tr></thead>
          <tbody>{rows.map((t) => <tr key={t.id}><th scope="row">{t.name}</th><td>{t.customer_name}</td><td>{t.frequency}</td><td>{t.next_run}</td><td className="num">{formatCents(t.total_cents)}</td></tr>)}</tbody>
        </table></div>
      )}
    </>
  );
}
