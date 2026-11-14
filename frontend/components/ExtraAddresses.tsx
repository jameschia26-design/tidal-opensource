"use client";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type Addr = { attention: string; line1: string; line2: string; city: string; region: string; postal_code: string; country: string; phone: string; fax: string };
type Row = { id: number; label: string; address: Addr };
const blank = (): Addr => ({ attention: "", line1: "", line2: "", city: "", region: "", postal_code: "", country: "SG", phone: "", fax: "" });

// Extra named addresses (warehouse, branch...) kept beside the billing and shipping address.
export default function ExtraAddresses({ contactId, orgId, readOnly }: { contactId: number; orgId: number; readOnly: boolean }) {
  const live = useRef(true);
  const [rows, setRows] = useState<Row[] | null>(null);
  const [label, setLabel] = useState("");
  const [addr, setAddr] = useState(blank);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const base = `/v1/contacts/${contactId}/addresses?org_id=${orgId}`;

  async function load() { const r = await api<{ addresses: Row[] }>(base); if (live.current) setRows(r.addresses); }
  useEffect(() => {
    live.current = true;
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function run(fn: () => Promise<unknown>) {
    if (busy) return;
    setBusy(true); setError("");
    try { await fn(); await load(); return true; }
    catch (e) { const x = e as ApiError; if (live.current) setError(messageFor(x.error, x.status)); }
    finally { if (live.current) setBusy(false); }
  }
  async function add(e: FormEvent) {
    e.preventDefault();
    if (await run(() => api(base, { method: "POST", body: JSON.stringify({ label: label.trim(), address: addr }) }))) { setOpen(false); setLabel(""); setAddr(blank()); }
  }
  const set = (k: keyof Addr, id: string, text: string, max: number) => <div><label htmlFor={id}>{text}</label><input id={id} maxLength={max} value={addr[k]} onChange={e => setAddr({ ...addr, [k]: k === "country" ? e.target.value.toUpperCase() : e.target.value })} /></div>;

  return <section aria-label="Additional addresses"><h3>Additional addresses</h3>
    {error && <p role="alert" className="form-error">{error}</p>}
    {rows === null && !error && <p role="status">Loading addresses…</p>}
    {rows?.length === 0 && <p className="section-help">None yet. Billing and shipping addresses are on the record itself.</p>}
    {!!rows?.length && <ul>{rows.map(r => <li key={r.id}><strong>{r.label}</strong>: {[r.address.attention, r.address.line1, r.address.line2, r.address.city, r.address.region, r.address.postal_code, r.address.country].filter(Boolean).join(", ")}{!readOnly && <> <button type="button" className="button-secondary" disabled={busy} onClick={() => void run(() => api(`/v1/contact-addresses/${r.id}?org_id=${orgId}`, { method: "DELETE" }))}>Remove<span className="sr-only"> {r.label}</span></button></>}</li>)}</ul>}
    {!readOnly && !open && <button type="button" className="button-secondary" onClick={() => setOpen(true)}>Add address</button>}
    {!readOnly && open && <form onSubmit={add}><fieldset disabled={busy}><legend>New address</legend><div className="form-grid">
      <div><label htmlFor={`xa-label-${contactId}`}>Label</label><input id={`xa-label-${contactId}`} required maxLength={100} value={label} onChange={e => setLabel(e.target.value)} /></div>
      {set("attention", `xa-att-${contactId}`, "Attention", 200)}{set("line1", `xa-l1-${contactId}`, "Address line 1", 200)}{set("line2", `xa-l2-${contactId}`, "Address line 2", 200)}
      {set("city", `xa-city-${contactId}`, "City", 200)}{set("region", `xa-reg-${contactId}`, "State / region", 200)}{set("postal_code", `xa-pc-${contactId}`, "Postal code", 20)}
      {set("country", `xa-cty-${contactId}`, "Country (2-letter code)", 2)}{set("phone", `xa-ph-${contactId}`, "Phone", 40)}{set("fax", `xa-fax-${contactId}`, "Fax", 40)}
    </div><div className="form-actions"><button>{busy ? "Saving…" : "Save address"}</button><button type="button" className="button-secondary" onClick={() => setOpen(false)}>Cancel</button></div></fieldset></form>}
  </section>;
}
