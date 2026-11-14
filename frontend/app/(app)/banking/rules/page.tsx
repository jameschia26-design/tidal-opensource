"use client";
import { useEffect, useState, type FormEvent } from "react";
import Link from "next/link";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { TrackingFields } from "@/components/TrackingSelect";

type Account = { id: number; code: string; name: string };
type Rule = { id: number; name: string; direction: string; contains_text: string; account_id: number; active: boolean; amount_min_cents: number | null; amount_max_cents: number | null; reference_contains: string | null; priority: number; condition_match: string; contact_id: number | null; tax_rate_code: string | null; tracking: Record<string, number>; conditions: { field: string; value: string }[] };
type Contact = { id: number; name: string };
type TaxRate = { code: string; name: string };

export default function BankRules() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}
function Workspace() {
  const { selected } = useOrg(); const orgId = selected?.id;
  const [accounts, setAccounts] = useState<Account[]>([]); const [rules, setRules] = useState<Rule[]>([]);
  const [name, setName] = useState(""); const [text, setText] = useState(""); const [direction, setDirection] = useState("spend"); const [account, setAccount] = useState("");
  const [minAmount, setMinAmount] = useState(""); const [maxAmount, setMaxAmount] = useState(""); const [refText, setRefText] = useState(""); const [priority, setPriority] = useState("100");
  const [conds, setConds] = useState<{ field: string; value: string }[]>([]);
  const [conditionMatch, setConditionMatch] = useState("all");
  const [contact, setContact] = useState(""); const [tax, setTax] = useState(""); const [tracking, setTracking] = useState<Record<string, number>>({});
  const [contacts, setContacts] = useState<Contact[]>([]); const [rates, setRates] = useState<TaxRate[]>([]);
  const [editing, setEditing] = useState<Rule | null>(null);
  const [busy, setBusy] = useState(false); const [error, setError] = useState(""); const [notice, setNotice] = useState("");
  const canWrite = selected && ["owner", "admin", "accountant"].includes(selected.role);
  // Amounts are typed in dollars and sent as cents; blank means "no condition".
  function cents(v: string): number | null {
    const t = v.trim(); if (!t) return null;
    const n = Number(t); return Number.isFinite(n) && n >= 0 ? Math.round(n * 100) : null;
  }
  function ruleBody(id: number) {
    return JSON.stringify({ org_id: id, name, direction, contains_text: text, account_id: Number(account),
      amount_min_cents: cents(minAmount), amount_max_cents: cents(maxAmount),
      reference_contains: refText.trim() || null, priority: Number(priority) || 100,
      condition_match: conditionMatch, contact_id: contact ? Number(contact) : null,
      tax_rate_code: tax || null, tracking,
      conditions: conds.filter(c => c.value.trim()) });
  }
  async function load(id: number) {
    const [a, r, c, t] = await Promise.all([api<{ accounts: Account[] }>(`/v1/accounts?org_id=${id}`), api<{ rules: Rule[] }>(`/v1/bank/rules?org_id=${id}`),
      api<{ contacts: Contact[] }>(`/v1/contacts?org_id=${id}`), api<{ rates: TaxRate[] }>(`/v1/tax/rates?org_id=${id}`)]);
    setAccounts(a.accounts); setRules(r.rules); setContacts(c.contacts); setRates(t.rates);
  }
  useEffect(() => { if (orgId != null) void load(orgId).catch((e: ApiError) => setError(messageFor(e.error, e.status))); }, [orgId]);
  async function save(e: FormEvent) {
    e.preventDefault(); if (!orgId || !canWrite || busy) return;
    setBusy(true); setError(""); setNotice("");
    try {
      if (editing) {
        await api(`/v1/bank/rules/${editing.id}`, { method: "PUT", body: ruleBody(orgId) });
        setNotice(`Rule "${name}" updated.`); cancelEdit();
      } else {
        await api("/v1/bank/rules", { method: "POST", body: ruleBody(orgId) });
        clearForm(); setNotice("Rule saved. It suggests an account; it does not post transactions.");
      }
      await load(orgId);
    } catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } finally { setBusy(false); }
  }
  async function toggle(rule: Rule) {
    if (!orgId || !canWrite || busy) return;
    setBusy(true); setError("");
    try { await api("/v1/bank/rules/active", { method: "POST", body: JSON.stringify({ org_id: orgId, id: rule.id, active: !rule.active }) }); await load(orgId); }
    catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } finally { setBusy(false); }
  }
  function clearForm() { setName(""); setText(""); setDirection("spend"); setAccount(""); setMinAmount(""); setMaxAmount(""); setRefText(""); setPriority("100"); setConds([]); setConditionMatch("all"); setContact(""); setTax(""); setTracking({}); }
  function startEdit(rule: Rule) {
    setEditing(rule); setName(rule.name); setText(rule.contains_text); setDirection(rule.direction); setAccount(String(rule.account_id));
    setMinAmount(rule.amount_min_cents == null ? "" : (rule.amount_min_cents / 100).toFixed(2));
    setMaxAmount(rule.amount_max_cents == null ? "" : (rule.amount_max_cents / 100).toFixed(2));
    setRefText(rule.reference_contains ?? ""); setPriority(String(rule.priority ?? 100)); setConds(rule.conditions ?? []); setError(""); setNotice("");
    setConditionMatch(rule.condition_match ?? "all"); setContact(rule.contact_id ? String(rule.contact_id) : ""); setTax(rule.tax_rate_code ?? ""); setTracking(rule.tracking ?? {});
  }
  function cancelEdit() { setEditing(null); clearForm(); }
  async function remove(rule: Rule) {
    if (!orgId || !canWrite || busy) return;
    if (!window.confirm(`Delete rule "${rule.name}"? Imported lines will no longer suggest its account.`)) return;
    setBusy(true); setError(""); setNotice("");
    try { await api(`/v1/bank/rules/${rule.id}?org_id=${orgId}`, { method: "DELETE" }); if (editing?.id === rule.id) cancelEdit(); setNotice("Rule deleted."); await load(orgId); }
    catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } finally { setBusy(false); }
  }
  return <><PageHeader title="Bank rules" description="Suggest an account when an imported statement description contains specified text. Rules do not post transactions." actions={<Link href="/banking">Banking</Link>} />
    {!selected && <p>Choose a company first.</p>}{error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {canWrite && <form className="record-form" onSubmit={save}><fieldset disabled={busy}><legend>{editing ? `Edit bank rule: ${editing.name}` : "New bank rule"}</legend>
      <label htmlFor="rule-name">Name</label><input id="rule-name" value={name} maxLength={100} required onChange={e => setName(e.target.value)} />
      <label htmlFor="rule-direction">Direction</label><select id="rule-direction" value={direction} onChange={e => setDirection(e.target.value)}><option value="spend">Spend money</option><option value="receive">Receive money</option><option value="transfer">Transfer money</option></select>
      <label htmlFor="rule-text">Description contains</label><input id="rule-text" value={text} maxLength={100} required onChange={e => setText(e.target.value)} />
      <label htmlFor="rule-min">Amount at least (SGD, optional)</label><input id="rule-min" inputMode="decimal" value={minAmount} onChange={e => setMinAmount(e.target.value)} />
      <label htmlFor="rule-max">Amount at most (SGD, optional)</label><input id="rule-max" inputMode="decimal" value={maxAmount} onChange={e => setMaxAmount(e.target.value)} />
      <label htmlFor="rule-ref">Reference contains (optional)</label><input id="rule-ref" value={refText} maxLength={100} onChange={e => setRefText(e.target.value)} />
      <label htmlFor="rule-priority">Priority (1 runs first, 1–999)</label><input id="rule-priority" type="number" min={1} max={999} value={priority} onChange={e => setPriority(e.target.value)} />
      <label htmlFor="rule-match">Conditions match</label><select id="rule-match" value={conditionMatch} onChange={e => setConditionMatch(e.target.value)}><option value="all">All conditions (and)</option><option value="any">Any condition (or)</option></select>
      <fieldset><legend>Extra conditions</legend>
        {conds.map((c, i) => <div key={i} className="form-grid"><div><label htmlFor={`rule-cond-field-${i}`}>Field</label><select id={`rule-cond-field-${i}`} value={c.field} onChange={e => setConds(conds.map((v, j) => j === i ? { ...v, field: e.target.value } : v))}><option value="description">Description</option><option value="reference">Reference</option></select></div>
          <div><label htmlFor={`rule-cond-value-${i}`}>Contains</label><input id={`rule-cond-value-${i}`} value={c.value} maxLength={100} onChange={e => setConds(conds.map((v, j) => j === i ? { ...v, value: e.target.value } : v))} /></div>
          <div><button type="button" className="button-secondary" onClick={() => setConds(conds.filter((_, j) => j !== i))}>Remove condition {i + 1}</button></div></div>)}
        <button type="button" className="button-secondary" disabled={conds.length >= 10} onClick={() => setConds([...conds, { field: "description", value: "" }])}>Add condition</button>
        <p className="section-help">The description text above and these conditions are grouped by the All/Any choice. A CSV statement has no payee column, so conditions cover description and reference text.</p>
      </fieldset>
      <label htmlFor="rule-account">{direction === "transfer" ? "Other bank account" : "Suggested account"}</label><select id="rule-account" value={account} required onChange={e => setAccount(e.target.value)}><option value="">Choose…</option>{accounts.map(a => <option key={a.id} value={a.id}>{a.code} · {a.name}</option>)}</select>
      <fieldset><legend>Suggested actions (shown on the suggestion, never posted automatically)</legend>
        <label htmlFor="rule-contact">Contact</label><select id="rule-contact" value={contact} onChange={e => setContact(e.target.value)}><option value="">No contact</option>{contacts.map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select>
        <label htmlFor="rule-tax">Tax rate</label><select id="rule-tax" value={tax} onChange={e => setTax(e.target.value)}><option value="">No tax rate</option>{rates.map(r => <option key={r.code} value={r.code}>{r.code} · {r.name}</option>)}</select>
        {orgId != null && <TrackingFields orgId={orgId} idPrefix="rule" lineNumber={1} value={tracking} onChange={setTracking} />}
      </fieldset>
      <div className="form-actions"><button type="submit">{editing ? "Save changes" : "Save rule"}</button>{editing && <button type="button" className="button-secondary" disabled={busy} onClick={cancelEdit}>Cancel</button>}</div></fieldset></form>}
    {selected && <div className="table-scroll"><table className="doc-table"><caption>Bank rules (lowest priority number wins; conditions other than the description are optional)</caption><thead><tr><th>Name</th><th>Priority</th><th>Direction</th><th>Conditions</th><th>Suggested account</th><th>Status</th>{canWrite && <th>Action</th>}</tr></thead><tbody>{rules.map(r => <tr key={r.id}><th scope="row">{r.name}</th><td>{r.priority}</td><td>{r.direction}</td><td>Description contains “{r.contains_text}”{r.amount_min_cents != null && `, amount ≥ ${(r.amount_min_cents / 100).toFixed(2)}`}{r.amount_max_cents != null && `, amount ≤ ${(r.amount_max_cents / 100).toFixed(2)}`}{r.reference_contains && `, reference contains “${r.reference_contains}”`}{r.conditions?.map(c => `, ${c.field} contains “${c.value}”`).join("")} — match {r.condition_match === "any" ? "any" : "all"}{r.contact_id ? ", contact suggested" : ""}{r.tax_rate_code ? `, tax ${r.tax_rate_code}` : ""}{r.tracking && Object.keys(r.tracking).length ? ", tracking suggested" : ""}</td><td>{accounts.find(a => a.id === r.account_id)?.name ?? r.account_id}</td><td>{r.active ? "Active" : "Inactive"}</td>{canWrite && <td><button className="button-secondary" disabled={busy} onClick={() => void toggle(r)}>{r.active ? "Deactivate" : "Activate"} {r.name}</button> <button className="button-secondary" disabled={busy} onClick={() => startEdit(r)}>Edit</button> <button className="button-secondary" disabled={busy} onClick={() => void remove(r)}>Delete</button></td>}</tr>)}</tbody></table></div>}
  </>;
}
