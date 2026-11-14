"use client";
import { useEffect, useRef, useState, type FormEvent } from "react";
import Link from "next/link";
import PageHeader from "@/components/PageHeader";
import DateInput from "@/components/DateInput";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { parseCents, formatCents, formatMoneyInput } from "@/lib/money";
import { formatDate, todayISO } from "@/lib/dates";

type Credit = { id: number; credit_number: string; contact_id: number; supplier_name: string; credit_date: string; reference: string; net_cents: number; gst_cents: number; total_cents: number; status: string; currency: string; fx_rate_micro: number; bill_id: number; bill_number: string };
type Contact = { id: number; name: string };
type Account = { id: number; code: string; name: string; type: string };
type Bill = { id: number; document_number: string; supplier_name: string; supplier_reference: string; balance_cents: number; currency: string };
type Currency = { code: string; name: string; is_base: boolean; active: boolean; rates: { rate_date: string; rate_micro: number }[] };

export default function SupplierCreditsPage() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function Workspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const live = useRef(true);
  const [rows, setRows] = useState<Credit[] | null>(null);
  const [contacts, setContacts] = useState<Contact[]>([]);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [bills, setBills] = useState<Bill[]>([]);
  const [currencies, setCurrencies] = useState<Currency[]>([]);
  const [currency, setCurrency] = useState("SGD");
  const [supplier, setSupplier] = useState("");
  const [billId, setBillId] = useState("");
  const [account, setAccount] = useState("");
  const [date, setDate] = useState(todayISO());
  const [reference, setReference] = useState("");
  const [net, setNet] = useState("");
  const [gst, setGst] = useState("");
  type CreditLine = { account_id: string; description: string; net: string };
  const blankCreditLine = (): CreditLine => ({ account_id: "", description: "", net: "" });
  const [creditLines, setCreditLines] = useState<CreditLine[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  async function load() {
    if (orgId == null) return;
    const [c, a, b, s, fx] = await Promise.all([
      api<{ contacts: Contact[] }>(`/v1/contacts?org_id=${orgId}&role=supplier`),
      api<{ accounts: Account[] }>(`/v1/accounts?org_id=${orgId}`),
      api<{ bills: Bill[] }>(`/v1/bills?org_id=${orgId}&status=awaiting_payment`),
      api<{ supplier_credits: Credit[] }>(`/v1/supplier-credits?org_id=${orgId}`),
      api<{ currencies: Currency[] }>(`/v1/currencies?org_id=${orgId}`),
    ]);
    if (!live.current) return;
    setContacts(c.contacts); setAccounts(a.accounts.filter(x => x.type === "expense")); setBills(b.bills); setRows(s.supplier_credits); setCurrencies(fx.currencies.filter(x => x.active));
  }
  useEffect(() => {
    live.current = true;
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const netCents = parseCents(net || "0");
  const gstCents = parseCents(gst || "0");
  const total = (netCents ?? 0) + (gstCents ?? 0);
  const parsedLines = creditLines.map(l => ({ account_id: Number(l.account_id), description: l.description.trim(), net_cents: parseCents(l.net) }));
  const linesSum = parsedLines.reduce((n, l) => n + (l.net_cents ?? 0), 0);
  const linesOn = billId === "" && creditLines.length > 0;
  const linesOk = !linesOn || (parsedLines.length >= 1 && parsedLines.length <= 200 && parsedLines.every(l => l.account_id > 0 && l.description !== "" && l.net_cents !== null && l.net_cents > 0) && netCents !== null && linesSum === netCents);
  const datedRates = currencies.find(c => c.code === currency)?.rates.filter(r => r.rate_date <= date) ?? [];
  const selectedRate = currency === "SGD" ? 1_000_000 : datedRates.reduce((latest, rate) => rate.rate_date > (latest?.rate_date ?? "") ? rate : latest, undefined as Currency["rates"][number] | undefined)?.rate_micro;
  const selectedBill = bills.find(b => String(b.id) === billId);
  const valid = supplier !== "" && date !== "" && reference.trim() !== "" && netCents !== null && gstCents !== null && total > 0 && (billId !== "" || (linesOn ? linesOk : account !== "")) && linesOk && selectedRate !== undefined && (!selectedBill || selectedBill.currency === currency);
  async function create(e: FormEvent) {
    e.preventDefault(); if (busy || !valid) return;
    setBusy(true); setError(""); setNotice("");
    try {
      const made = await api<{ credit_number: string }>("/v1/supplier-credits", { method: "POST", body: JSON.stringify({ org_id: orgId, contact_id: Number(supplier), bill_id: billId ? Number(billId) : 0, account_id: linesOn ? 0 : (account ? Number(account) : 0), credit_date: date, reference: reference.trim(), net_cents: netCents, gst_cents: gstCents, ...(linesOn ? { lines: parsedLines } : {}), ...(currency === "SGD" ? {} : { currency }) }) });
      if (!live.current) return;
      setNotice(`${made.credit_number} saved as a draft. Approving it posts the credit.`);
      setReference(""); setNet(""); setGst(""); setBillId(""); setAccount(""); setCreditLines([]); setCurrency("SGD"); await load();
    } catch (e) { if (live.current) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } }
    finally { if (live.current) setBusy(false); }
  }
  async function approve(c: Credit) {
    if (busy) return;
    setBusy(true); setError(""); setNotice("");
    try {
      await api(`/v1/supplier-credits/${c.id}/approve`, { method: "POST", body: JSON.stringify({ org_id: orgId }) });
      if (!live.current) return;
      setNotice(`${c.credit_number} approved and posted.`); await load();
    } catch (e) { if (live.current) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } }
    finally { if (live.current) setBusy(false); }
  }

  return <>
    <PageHeader title="Supplier credits" description="A credit from a supplier has its own number and list. A draft is posted only when it is approved." />
    <p className="notice">Link the credit to a posted bill to reduce what that bill still owes, or leave the bill empty and choose the expense account the credit reverses. Refunds are recorded from the bill corrections panel.</p>
    {!selected && <p>Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {selected && canWrite && <form className="record-form" onSubmit={create}><fieldset disabled={busy}><legend>New supplier credit</legend><div className="form-grid">
      <div><label htmlFor="sc-supplier">Supplier</label><select id="sc-supplier" required value={supplier} onChange={e => { setSupplier(e.target.value); setBillId(""); }}><option value="">Choose supplier</option>{contacts.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select></div>
      <div><label htmlFor="sc-bill">Against a bill <span className="field-optional">Optional</span></label><select id="sc-bill" value={billId} onChange={e => setBillId(e.target.value)}><option value="">No bill — standalone</option>{bills.filter(b => b.supplier_name === contacts.find(c => String(c.id) === supplier)?.name).map(b => <option key={b.id} value={b.id} disabled={b.currency !== currency}>{b.document_number} — {b.supplier_reference} ({formatCents(b.balance_cents)} {b.currency} owed)</option>)}</select><p className="section-help">Linking takes the expense split from the bill.</p></div>
      <div><label htmlFor="sc-account">Expense account <span className="field-optional">Standalone credits only</span></label><select id="sc-account" disabled={billId !== "" || linesOn} value={account} onChange={e => setAccount(e.target.value)}><option value="">Choose account</option>{accounts.map(a => <option key={a.id} value={a.id}>{a.code} — {a.name}</option>)}</select>{linesOn && <p className="section-help">Using the line grid below instead.</p>}</div>
      <div><label htmlFor="sc-date">Date</label><DateInput id="sc-date" required value={date} onChange={e => setDate(e.target.value)} /></div>
      <div><label htmlFor="sc-currency">Currency</label><select id="sc-currency" value={currency} onChange={e => setCurrency(e.target.value)}>{currencies.map(c => <option key={c.code} value={c.code}>{c.code} — {c.name}</option>)}</select><p className="section-help">Uses latest saved rate on or before credit date. A dated rate is required for foreign currency.</p>{currency !== "SGD" && <p className="section-help">Rate to SGD: {selectedRate ? `${(selectedRate / 1_000_000).toFixed(6)}` : "No rate available for this date"}</p>}</div>
      <div><label htmlFor="sc-reference">Reference / reason</label><input id="sc-reference" required maxLength={100} value={reference} onChange={e => setReference(e.target.value)} /></div>
      <div><label htmlFor="sc-net">Net amount ({currency})</label><input id="sc-net" inputMode="decimal" required value={net} onChange={e => setNet(formatMoneyInput(e.target.value))} /></div>
      <div><label htmlFor="sc-gst">GST amount ({currency})</label><input id="sc-gst" inputMode="decimal" value={gst} onChange={e => setGst(formatMoneyInput(e.target.value))} /></div>
    </div>{billId === "" && <fieldset disabled={busy}><legend>Line grid <span className="field-optional">Optional</span></legend><p className="section-help">Split the net across expense accounts. Lines must foot to the net amount above; GST stays header-level. Leave empty for a single expense account.</p>{creditLines.map((l, i) => <div className="form-grid" key={i}><div><label htmlFor={`sc-line-account-${i}`}>Account</label><select id={`sc-line-account-${i}`} value={l.account_id} onChange={e => setCreditLines(creditLines.map((v, j) => i === j ? { ...v, account_id: e.target.value } : v))}><option value="">Choose account</option>{accounts.map(a => <option key={a.id} value={a.id}>{a.code} — {a.name}</option>)}</select></div><div><label htmlFor={`sc-line-desc-${i}`}>Description</label><input id={`sc-line-desc-${i}`} maxLength={200} value={l.description} onChange={e => setCreditLines(creditLines.map((v, j) => i === j ? { ...v, description: e.target.value } : v))} /></div><div><label htmlFor={`sc-line-net-${i}`}>Net ({currency})</label><input id={`sc-line-net-${i}`} inputMode="decimal" value={l.net} onChange={e => setCreditLines(creditLines.map((v, j) => i === j ? { ...v, net: formatMoneyInput(e.target.value) } : v))} /></div><div><button type="button" className="button-secondary" onClick={() => setCreditLines(creditLines.filter((_, j) => j !== i))}>Remove line {i + 1}</button></div></div>)}<button type="button" className="button-secondary" disabled={creditLines.length >= 200} onClick={() => setCreditLines([...creditLines, blankCreditLine()])}>Add a new line</button>{linesOn && <p aria-live="polite">Lines total {formatCents(linesSum)} of net {formatCents(netCents ?? 0)}{linesOk ? "" : " — lines must foot to the net."}</p>}</fieldset>}<p aria-live="polite">Credit total {formatCents(total)}</p><div className="form-actions"><button disabled={busy || !valid}>{busy ? "Saving…" : "Save draft"}</button></div></fieldset></form>}
    {rows === null && !error && <p role="status">Loading supplier credits…</p>}{rows?.length === 0 && <p>No supplier credits yet.</p>}
    {!!rows?.length && <div className="table-scroll"><table className="doc-table"><caption>Supplier credits</caption><thead><tr><th scope="col">Number</th><th scope="col">Supplier</th><th scope="col">Date</th><th scope="col">Reference</th><th scope="col">Bill</th><th scope="col">Status</th><th scope="col" className="num">Net</th><th scope="col" className="num">GST</th><th scope="col" className="num">Total</th><th scope="col">Action</th></tr></thead><tbody>{rows.map(c => <tr key={c.id}><td>{c.credit_number}</td><td>{c.supplier_name}</td><td>{formatDate(c.credit_date)}</td><td>{c.reference}</td><td>{c.bill_number || "—"}</td><td>{c.status}</td><td className="num">{formatCents(c.net_cents)}</td><td className="num">{formatCents(c.gst_cents)}</td><td className="num">{formatCents(c.total_cents)} {c.currency !== "SGD" ? c.currency : ""}</td><td>{canWrite && c.status === "draft" && <button className="button-secondary" disabled={busy} onClick={() => void approve(c)}>Approve<span className="sr-only"> {c.credit_number}</span></button>}</td></tr>)}</tbody></table></div>}
    <p className="section-help">Supplier credits recorded from a bill are listed here too. <Link href="/purchases">Back to bills</Link></p>
  </>;
}
