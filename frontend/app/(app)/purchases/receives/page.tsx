"use client";
import { useEffect, useRef, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import Attachments from "@/components/Attachments";
import Extras from "@/components/Extras";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import DateInput from "@/components/DateInput";
import { todayISO } from "@/lib/dates";

type Line = { line_no: number; description: string; quantity: number };
type PO = { id: number; vendor_name: string; po_number: string; lines: Line[]; status: string; received: Record<string, number>; in_transit: Record<string, number> };
type Receive = { id: number; purchase_order_id: number; po_number: string; vendor_name: string; receive_number: string; received_date: string; tracking_number: string; tracking_url: string; items: { line_no: number; quantity: number }[]; notes: string; status: string };
const today = todayISO;
const blank = () => ({ po: "", receive_number: "", received_date: today(), tracking_number: "", tracking_url: "", notes: "" });

export default function PurchaseReceivesPage() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}

function Workspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const live = useRef(true);
  const [rows, setRows] = useState<Receive[] | null>(null);
  const [orders, setOrders] = useState<PO[]>([]);
  const [form, setForm] = useState(blank);
  const [qty, setQty] = useState<Record<number, string>>({});
  const [detail, setDetail] = useState<Receive | null>(null);
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const po = orders.find(o => o.id === Number(form.po));

  async function load() {
    if (orgId == null) return;
    const [r, p] = await Promise.all([api<{ purchase_receives: Receive[] }>(`/v1/purchase-receives?org_id=${orgId}`), api<{ purchase_orders: PO[] }>(`/v1/purchase-orders?org_id=${orgId}`)]);
    if (!live.current) return;
    setRows(r.purchase_receives); setOrders(p.purchase_orders);
    setDetail(d => d && (r.purchase_receives.find(x => x.id === d.id) ?? null));
  }
  useEffect(() => {
    live.current = true;
    void load().catch((e: ApiError) => { if (live.current) setError(messageFor(e.error, e.status)); });
    return () => { live.current = false; };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  async function send(path: string, body: object, done: string) {
    if (busy || orgId == null) return false;
    setBusy(true); setError(""); setNotice("");
    try {
      await api(path, { method: "POST", body: JSON.stringify({ org_id: orgId, ...body }) });
      if (!live.current) return false;
      setNotice(done); await load(); return true;
    } catch (e) { if (live.current) { const x = e as ApiError; setError(messageFor(x.error, x.status)); } return false; }
    finally { if (live.current) setBusy(false); }
  }
  async function save(e: FormEvent, status: "draft" | "received") {
    e.preventDefault();
    const items = (po?.lines ?? []).map(l => ({ line_no: l.line_no, quantity: /^\d{1,7}$/.test(qty[l.line_no] ?? "") ? Number(qty[l.line_no]) : 0 })).filter(i => i.quantity > 0);
    if (!po || !form.received_date || items.length === 0) { setError("Choose an issued purchase order and enter a quantity to receive on at least one item."); return; }
    if (await send("/v1/purchase-receives", { purchase_order_id: po.id, receive_number: form.receive_number.trim(), received_date: form.received_date, tracking_number: form.tracking_number.trim(), tracking_url: form.tracking_url.trim(), items, notes: form.notes, status }, status === "received" ? "Goods recorded as received." : "Receive saved as draft; quantities show as in transit.")) { setOpen(false); setForm(blank()); setQty({}); }
  }
  const stage = (r: Receive, to: string, msg: string) => void send(`/v1/purchase-receives/${r.id}/stage`, { stage: to }, msg);
  const safeLink = (u: string) => /^https?:\/\//i.test(u);

  return <>
    <PageHeader title="Purchase receives" description="Record goods received against an issued purchase order." />
    <p className="notice">A receive records delivery only: no stock is tracked and no journal is posted. Enter the supplier bill separately. Saved receives cannot be edited; cancel a draft and enter a new one.</p>
    {!selected && <p>Choose a company first.</p>}
    {error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    {selected && canWrite && <button disabled={busy || open} onClick={() => { setOpen(true); setDetail(null); setForm(blank()); setQty({}); }}>New purchase receive</button>}
    {open && canWrite && <form className="record-form" onSubmit={e => void save(e, "received")}>
      <fieldset disabled={busy}><legend>Purchase receive</legend><div className="form-grid">
        <div><label htmlFor="pr-po">Purchase order#</label><select id="pr-po" required value={form.po} onChange={e => { setForm({ ...form, po: e.target.value }); setQty({}); }}><option value="">Choose issued order</option>{orders.filter(o => o.status === "issued").map(o => <option key={o.id} value={o.id}>{o.po_number} — {o.vendor_name}</option>)}</select></div>
        <div><label htmlFor="pr-vendor">Vendor</label><input id="pr-vendor" readOnly value={po?.vendor_name ?? ""} /></div>
        <div><label htmlFor="pr-number">Purchase receive# (blank = automatic)</label><input id="pr-number" maxLength={50} value={form.receive_number} onChange={e => setForm({ ...form, receive_number: e.target.value })} /></div>
        <div><label htmlFor="pr-date">Received date</label><DateInput id="pr-date" required value={form.received_date} onChange={e => setForm({ ...form, received_date: e.target.value })}/></div>
        <div><label htmlFor="pr-track">Tracking#</label><input id="pr-track" maxLength={100} value={form.tracking_number} onChange={e => setForm({ ...form, tracking_number: e.target.value })} /></div>
        <div><label htmlFor="pr-url">Tracking URL</label><input id="pr-url" type="url" maxLength={500} value={form.tracking_url} onChange={e => setForm({ ...form, tracking_url: e.target.value })} /></div>
      </div></fieldset>
      {po && <fieldset disabled={busy}><legend>Items</legend><div className="table-scroll"><table className="doc-table"><caption>Quantities</caption><thead><tr><th>Items &amp; description</th><th>Ordered</th><th>Received</th><th>In transit</th><th>Quantity to receive</th></tr></thead><tbody>{po.lines.map(l => { const got = po.received?.[l.line_no] ?? 0, moving = po.in_transit?.[l.line_no] ?? 0; return <tr key={l.line_no}><td>{l.description}</td><td>{l.quantity}</td><td>{got}</td><td>{moving}</td><td><input aria-label={`Quantity to receive, ${l.description}`} inputMode="numeric" disabled={got + moving >= l.quantity} value={qty[l.line_no] ?? ""} onChange={e => setQty({ ...qty, [l.line_no]: e.target.value })} /></td></tr>; })}</tbody></table></div></fieldset>}
      <fieldset disabled={busy}><legend>Notes (for internal use)</legend><textarea aria-label="Notes" rows={3} maxLength={4000} value={form.notes} onChange={e => setForm({ ...form, notes: e.target.value })} /><p className="section-help">Save first, then attach up to 5 delivery notes (10 MB each) from the receive details.</p></fieldset>
      <div className="form-actions"><button disabled={busy}>{busy ? "Saving…" : "Save as received"}</button><button type="button" disabled={busy} className="button-secondary" onClick={e => void save(e as unknown as FormEvent, "draft")}>Save as draft</button><button type="button" className="button-secondary" disabled={busy} onClick={() => setOpen(false)}>Cancel</button></div>
    </form>}
    {detail && orgId != null && <section className="record-form"><h2>{detail.receive_number} — {detail.po_number}</h2>
      <dl className="detail-grid"><div><dt>Vendor</dt><dd>{detail.vendor_name}</dd></div><div><dt>Status</dt><dd>{detail.status}</dd></div><div><dt>Received</dt><dd>{detail.received_date}</dd></div><div><dt>Tracking</dt><dd>{detail.tracking_number || "—"}</dd></div></dl>
      {detail.tracking_url && (safeLink(detail.tracking_url) ? <p><a href={detail.tracking_url} target="_blank" rel="noopener noreferrer">Open tracking page</a></p> : <p>{detail.tracking_url}</p>)}
      <p>{detail.items.map(i => { const l = orders.find(o => o.id === detail.purchase_order_id)?.lines.find(x => x.line_no === i.line_no); return `${l?.description ?? `Item ${i.line_no}`} × ${i.quantity}`; }).join(", ")}</p>
      {detail.notes && <p className="long-text">{detail.notes}</p>}
      <Attachments key={detail.id} kind="purchase-receives" id={detail.id} orgId={orgId} limit={5} readOnly={!canWrite} /><Extras kind="purchase-receives" id={detail.id} orgId={orgId} readOnly={!canWrite} />
      <div className="form-actions">{canWrite && detail.status === "draft" && <><button disabled={busy} onClick={() => stage(detail, "received", "Goods recorded as received.")}>Mark received</button><button className="button-secondary" disabled={busy} onClick={() => stage(detail, "cancelled", "Draft cancelled.")}>Cancel draft</button></>}<button className="button-secondary" onClick={() => setDetail(null)}>Close details</button></div>
    </section>}
    {rows === null && !error && <p role="status">Loading purchase receives…</p>}{rows?.length === 0 && <p>No purchase receives yet.</p>}
    {!!rows?.length && <div className="table-scroll"><table className="doc-table"><caption>Latest 200 purchase receives</caption><thead><tr><th>Receive#</th><th>Purchase order</th><th>Vendor</th><th>Received</th><th>Status</th><th>Details</th></tr></thead><tbody>{rows.map(r => <tr key={r.id}><td>{r.receive_number}</td><td>{r.po_number}</td><td>{r.vendor_name}</td><td>{r.received_date}</td><td>{r.status}</td><td><button className="button-secondary" onClick={() => { setDetail(r); setOpen(false); }}>View<span className="sr-only"> {r.receive_number}</span></button></td></tr>)}</tbody></table></div>}
  </>;
}
