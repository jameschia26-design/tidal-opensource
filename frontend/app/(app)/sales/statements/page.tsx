"use client";
import { useEffect, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { formatCents } from "@/lib/money";
import DateInput from "@/components/DateInput";
import { formatDate } from "@/lib/dates";

type Contact = { id: number; name: string };
type Summary = { from: string; to: string; customers: { contact_id: number; name: string; email: string; account_number: string; address: Record<string, string>; balance_cents: number; overdue_cents: number }[] };
type Statement = { contact: string; account_number: string; address: Record<string, string>; from: string; to: string; brought_forward_cents: number; balance_cents: number; rows: { date: string; kind: string; description: string; charge_cents: number; credit_cents: number; balance_cents: number }[] };

export default function Statements() { const { selected } = useOrg(); return <Workspace key={selected?.id ?? "none"} />; }
function Workspace() {
  const { selected } = useOrg(); const orgId = selected?.id;
  const [contacts, setContacts] = useState<Contact[]>([]); const [customer, setCustomer] = useState(""); const [from, setFrom] = useState(""); const [to, setTo] = useState("");
  const [statement, setStatement] = useState<Statement | null>(null); const [error, setError] = useState(""); const [busy, setBusy] = useState(false);
  const [kind, setKind] = useState("activity");
  const [summary, setSummary] = useState<Summary | null>(null); const [picked, setPicked] = useState<number[]>([]); const [sendNote, setSendNote] = useState("");
  useEffect(() => { if (orgId == null) return; let live = true; api<{ contacts: Contact[] }>(`/v1/contacts?org_id=${orgId}&role=customer`).then(c => { if (live) setContacts(c.contacts); }).catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); }); return () => { live = false; }; }, [orgId]);
  async function run(e: FormEvent) {
    e.preventDefault(); if (!orgId || busy) return; setBusy(true); setError(""); setStatement(null); setSendNote("");
    try {
      const [one, list] = await Promise.all([
        api<Statement>(`/v1/statements?org_id=${orgId}&contact_id=${encodeURIComponent(customer)}&from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`),
        api<Summary>(`/v1/statements/summary?org_id=${orgId}&from=${encodeURIComponent(from)}&to=${encodeURIComponent(to)}`),
      ]);
      setStatement(one); setSummary(list); setPicked([]);
    }
    catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } finally { setBusy(false); }
  }
  // Sending reopens nothing: the statement already exists in the books, so a resend only emails it.
  async function send(ids: number[]) {
    if (!orgId || busy || !ids.length) return; setBusy(true); setError(""); setSendNote("");
    const failed: string[] = []; let sent = 0;
    for (const id of ids) {
      try { await api<{ sent_to: string }>("/v1/statements/send", { method: "POST", body: JSON.stringify({ org_id: orgId, contact_id: id, from, to }) }); sent += 1; }
      catch (e) { const x = e as ApiError; failed.push(`${contacts.find(c => c.id === id)?.name ?? id}: ${messageFor(x.error, x.status)}`); }
    }
    setBusy(false); setSendNote(`${sent} statement${sent === 1 ? "" : "s"} sent.`);
    if (failed.length) setError(failed.join(" "));
  }
  return <><PageHeader title="Customer statements" description="Activity statement for posted invoices, payments, credit notes and refunds." />
    {!selected && <p className="notice">Choose a company first.</p>}{error && <p role="alert" className="form-error">{error}</p>}
    {selected && <form className="record-form" onSubmit={run}><fieldset disabled={busy}><legend>Statement selection</legend><div className="form-grid"><div><label htmlFor="statement-type">Statement type</label><select id="statement-type" value={kind} onChange={e => { setKind(e.target.value); setStatement(null); }}><option value="activity">Activity statement</option><option value="balance">Balance statement</option></select><p className="section-help">Activity lists every movement. Balance lists what each customer owes as at the statement date, including overdue.</p></div>
      <div><label htmlFor="statement-customer">Customer</label><select required id="statement-customer" value={customer} onChange={e => { setCustomer(e.target.value); setStatement(null); }}><option value="">Choose customer</option>{contacts.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select></div>
      <div><label htmlFor="statement-from">From</label><DateInput required id="statement-from" value={from} onChange={e => { setFrom(e.target.value); setStatement(null); }}/></div>
      <div><label htmlFor="statement-to">To</label><DateInput required id="statement-to" min={from} value={to} onChange={e => { setTo(e.target.value); setStatement(null); }}/></div>
    </div></fieldset><button disabled={busy} type="submit">View statement</button></form>}
    {summary && <section className="record-form"><h2>Balances as at {formatDate(summary.to)}</h2>
      <p className="section-help">Every customer with a statement balance. Tick customers to email their statement; customers without an email address are refused and named.</p>
      <div className="table-scroll"><table className="doc-table"><caption>Customer statements (SGD)</caption><thead><tr><th>Send</th><th>Customer</th><th>Email</th><th>Balance</th><th><span className="visually-hidden">Actions</span></th></tr></thead><tbody>
        {summary.customers.map(c => <tr key={c.contact_id}><td><input type="checkbox" aria-label={`Send statement to ${c.name}`} checked={picked.includes(c.contact_id)} onChange={e => setPicked(e.target.checked ? [...picked, c.contact_id] : picked.filter(id => id !== c.contact_id))} /></td><td>{c.name}</td><td>{c.email || <span className="notice">No email</span>}</td><td className="num">{formatCents(c.balance_cents)}</td><td><button type="button" className="button-secondary" disabled={busy} onClick={() => send([c.contact_id])}>Send</button> <button type="button" className="button-secondary" disabled={busy} onClick={() => { setCustomer(String(c.contact_id)); setStatement(null); }}>View</button></td></tr>)}
      </tbody></table></div>
      <button type="button" disabled={busy || !picked.length} onClick={() => send(picked)}>Send selected</button>
      {sendNote && <p role="status">{sendNote}</p>}
    </section>}
    {summary && kind === "balance" && <section className="record-form"><h2>Balance statement as at {formatDate(summary.to)}</h2>
      <div className="table-scroll"><table className="doc-table"><caption>Customer balances (SGD)</caption><thead><tr><th>Name</th><th>Account No.</th><th>Address</th><th>Email</th><th className="num">Outstanding</th><th className="num">Overdue</th></tr></thead><tbody>
        {summary.customers.map(c => <tr key={c.contact_id}><td>{c.name}</td><td>{c.account_number || "—"}</td><td>{Object.values(c.address ?? {}).filter(Boolean).join(", ") || "—"}</td><td>{c.email || "—"}</td><td className="num">{formatCents(c.balance_cents)}</td><td className="num">{formatCents(c.overdue_cents)}</td></tr>)}
      </tbody></table></div><button type="button" className="button-secondary" onClick={() => window.print()}>Print</button>
      <p className="section-help">Emailing sends the activity statement for one customer; choose Activity to email.</p></section>}
    {statement && kind === "activity" && <section className="record-form"><h2>{statement.contact}</h2><p>{statement.account_number && <>Account {statement.account_number}. </>}{formatDate(statement.from)} to {formatDate(statement.to)}</p>
      {(statement.address && Object.values(statement.address).filter(Boolean).length > 0) ? <p>{Object.values(statement.address).filter(Boolean).join(", ")}</p> : null}
      <p className="notice">Includes the customer opening balance. Credit applications are not separate movements, because the full credit note is already included.</p>
      <div className="table-scroll"><table className="doc-table"><caption>Activity (SGD)</caption><thead><tr><th>Date</th><th>Details</th><th>Charge</th><th>Credit</th><th>Balance</th></tr></thead><tbody>
        <tr><td colSpan={4}>Brought forward</td><td>{formatCents(statement.brought_forward_cents)}</td></tr>
        {statement.rows.map((r, i) => <tr key={i}><td>{formatDate(r.date)}</td><td>{r.description}</td><td>{formatCents(r.charge_cents)}</td><td>{formatCents(r.credit_cents)}</td><td>{formatCents(r.balance_cents)}</td></tr>)}
      </tbody><tfoot><tr><th colSpan={4}>Balance</th><td>{formatCents(statement.balance_cents)}</td></tr></tfoot></table></div><button type="button" className="button-secondary" onClick={() => window.print()}>Print</button> <button type="button" className="button-secondary" disabled={busy} onClick={() => send([Number(customer)])}>Email to customer</button></section>}
  </>;
}
