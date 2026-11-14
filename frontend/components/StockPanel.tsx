"use client";
import { useEffect, useState, type FormEvent } from "react";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import { formatCents, parseCents } from "@/lib/money";
import DateInput from "@/components/DateInput";

type Tracked = { id: number; name: string; cost_price_cents: number };
type OnHand = { item_id: number; name: string; qty: number; value_cents: number };
type Adjustment = { id: number; item_name: string; adjustment_date: string; qty_delta: number; unit_cost_cents: number; reason: string; note: string; stocktake_id: number };
type Location = { id: number; name: string; is_default: boolean };
type LocationStock = { item_id: number; name: string; location_id: number; location: string; qty: number };

// Manual stock changes sit beside the item list: one adjustment at a time, or a stocktake that
// posts only the differences. Both call the same backend the inventory reports read.
export default function StockPanel({ orgId, items }: { orgId: number; items: Tracked[] }) {
  const [onHand, setOnHand] = useState<OnHand[]>([]);
  const [history, setHistory] = useState<Adjustment[]>([]);
  const [item, setItem] = useState("");
  const [date, setDate] = useState(new Date().toISOString().slice(0, 10));
  const [delta, setDelta] = useState("");
  const [cost, setCost] = useState("");
  const [reason, setReason] = useState("");
  const [note, setNote] = useState("");
  const [counted, setCounted] = useState<Record<number, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [locations, setLocations] = useState<Location[]>([]);
  const [byLocation, setByLocation] = useState<LocationStock[]>([]);
  const [newLocation, setNewLocation] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [transferQty, setTransferQty] = useState("");

  async function load() {
    try {
      const [stock, log, places, spread] = await Promise.all([
        api<{ rows: OnHand[] }>(`/v1/reports/inventory-items?org_id=${orgId}&as_of=${date}`),
        api<{ adjustments: Adjustment[] }>(`/v1/stock/adjustments?org_id=${orgId}`),
        api<{ locations: Location[] }>(`/v1/stock/locations?org_id=${orgId}`),
        api<{ rows: LocationStock[] }>(`/v1/reports/stock-by-location?org_id=${orgId}&as_of=${date}`),
      ]);
      setOnHand(stock.rows); setHistory(log.adjustments); setLocations(places.locations); setByLocation(spread.rows);
    } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
  }
  useEffect(() => { void load(); }, [orgId, date]); // eslint-disable-line react-hooks/exhaustive-deps

  async function adjust(e: FormEvent) {
    e.preventDefault();
    const amount = parseCents(cost || "0");
    if (busy || amount === null) return;
    setBusy(true); setError(""); setNotice("");
    try {
      const r = await api<{ on_hand: number }>("/v1/stock/adjustments", { method: "POST", body: JSON.stringify({ org_id: orgId, item_id: Number(item), adjustment_date: date, qty_delta: Number(delta), unit_cost_cents: amount, reason: reason.trim(), note }) });
      setNotice(`Stock adjusted. New quantity on hand: ${r.on_hand}.`);
      setDelta(""); setCost(""); setReason(""); setNote("");
      await load();
    } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
    finally { setBusy(false); }
  }

  async function stocktake(e: FormEvent) {
    e.preventDefault();
    if (busy) return;
    const lines = Object.entries(counted).filter(([, v]) => v !== "").map(([id, v]) => ({ item_id: Number(id), counted_qty: Number(v) }));
    if (!lines.length) { setError("Enter at least one counted quantity."); return; }
    setBusy(true); setError(""); setNotice("");
    try {
      const r = await api<{ items_counted: number; adjusted: number }>("/v1/stock/stocktakes", { method: "POST", body: JSON.stringify({ org_id: orgId, taken_on: date, note, lines }) });
      setNotice(`Stocktake saved. ${r.items_counted} items counted, ${r.adjusted} adjusted.`);
      setCounted({});
      await load();
    } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
    finally { setBusy(false); }
  }

  async function addLocation(e: FormEvent) {
    e.preventDefault();
    if (busy || !newLocation.trim()) return;
    setBusy(true); setError("");
    try {
      await api("/v1/stock/locations", { method: "POST", body: JSON.stringify({ org_id: orgId, name: newLocation.trim() }) });
      setNewLocation(""); await load();
    } catch (e) { const err = e as ApiError; setError(messageFor(err.error, err.status)); }
    finally { setBusy(false); }
  }

  async function transfer(e: FormEvent) {
    e.preventDefault();
    if (busy || !item || !from || !to || !transferQty) return;
    setBusy(true); setError(""); setNotice("");
    try {
      await api("/v1/stock/transfers", { method: "POST", body: JSON.stringify({ org_id: orgId, item_id: Number(item), from_location_id: Number(from), to_location_id: Number(to), transfer_date: date, qty: Number(transferQty), note }) });
      setNotice("Stock transferred."); setTransferQty(""); await load();
    } catch (e) { const err = e as ApiError; setError(err.error === "insufficient_stock" ? "That location does not hold enough stock on this date." : messageFor(err.error, err.status)); }
    finally { setBusy(false); }
  }

  if (!items.length) return <p className="notice">Mark a goods item as tracked before adjusting stock.</p>;
  return <section aria-label="Stock adjustments" className="record-form">
    <h2>Stock adjustments</h2>
    {error && <p role="alert" className="form-error">{error}</p>}{notice && <p role="status">{notice}</p>}
    <div className="table-scroll"><table className="doc-table"><caption>Quantity on hand at {date}</caption><thead><tr><th>Item</th><th>On hand</th><th>Value</th></tr></thead><tbody>
      {onHand.map(v => <tr key={v.item_id}><th scope="row">{v.name}</th><td>{v.qty}</td><td>{formatCents(v.value_cents)}</td></tr>)}
      {!onHand.length && <tr><td colSpan={3}>No tracked stock movements yet.</td></tr>}
    </tbody></table></div>
    <form onSubmit={adjust}><fieldset disabled={busy}><legend>Adjust one item</legend><div className="form-grid">
      <div><label htmlFor="stock-item">Item</label><select id="stock-item" required value={item} onChange={e => { setItem(e.target.value); const found = items.find(v => String(v.id) === e.target.value); setCost(found ? (found.cost_price_cents / 100).toFixed(2) : ""); }}><option value="">Choose tracked item</option>{items.map(v => <option key={v.id} value={v.id}>{v.name}</option>)}</select></div>
      <div><label htmlFor="stock-date">Date</label><DateInput id="stock-date" required value={date} onChange={e => setDate(e.target.value)}/></div>
      <div><label htmlFor="stock-delta">Change in quantity</label><input id="stock-delta" type="number" required step={1} value={delta} onChange={e => setDelta(e.target.value)} /><p className="section-help">Use a negative number to remove stock.</p></div>
      <div><label htmlFor="stock-cost">Unit cost (SGD)</label><input id="stock-cost" inputMode="decimal" value={cost} onChange={e => setCost(e.target.value)} /></div>
      <div><label htmlFor="stock-reason">Reason</label><input id="stock-reason" required maxLength={100} value={reason} onChange={e => setReason(e.target.value)} /></div>
      <div><label htmlFor="stock-note">Note <span className="field-optional">Optional</span></label><input id="stock-note" maxLength={4000} value={note} onChange={e => setNote(e.target.value)} /></div>
    </div><p className="form-actions"><button disabled={busy || !item || !delta || !reason.trim()}>Post adjustment</button></p></fieldset></form>
    <form onSubmit={stocktake}><fieldset disabled={busy}><legend>Stocktake</legend><p className="section-help">Only items whose counted quantity differs are adjusted.</p>
      <div className="table-scroll"><table className="doc-table"><caption>Counted quantities</caption><thead><tr><th>Item</th><th>On hand</th><th>Counted</th></tr></thead><tbody>
        {items.map(v => <tr key={v.id}><th scope="row">{v.name}</th><td>{onHand.find(h => h.item_id === v.id)?.qty ?? 0}</td><td><label className="visually-hidden" htmlFor={`count-${v.id}`}>Counted quantity for {v.name}</label><input id={`count-${v.id}`} type="number" step={1} min={0} value={counted[v.id] ?? ""} onChange={e => setCounted({ ...counted, [v.id]: e.target.value })} /></td></tr>)}
      </tbody></table></div>
      <div className="form-actions"><button disabled={busy}>Post stocktake</button></div>
    </fieldset></form>
    <h2>Locations</h2>
    <p className="section-help">Documents move stock at the default location. Transfers move quantity between locations and never change the total on hand.</p>
    <div className="table-scroll"><table className="doc-table"><caption>Quantity by location at {date}</caption><thead><tr><th>Item</th>{locations.map(l => <th key={l.id}>{l.name}{l.is_default ? " (default)" : ""}</th>)}</tr></thead><tbody>
      {items.map(v => <tr key={v.id}><th scope="row">{v.name}</th>{locations.map(l => <td key={l.id}>{byLocation.find(r => r.item_id === v.id && r.location_id === l.id)?.qty ?? 0}</td>)}</tr>)}
    </tbody></table></div>
    <form onSubmit={addLocation}><fieldset disabled={busy}><legend>Add a location</legend><div className="form-grid"><div><label htmlFor="location-name">Location name</label><input id="location-name" required maxLength={100} value={newLocation} onChange={e => setNewLocation(e.target.value)} /></div></div><p className="form-actions"><button disabled={busy || !newLocation.trim()}>Add location</button></p></fieldset></form>
    <form onSubmit={transfer}><fieldset disabled={busy}><legend>Transfer stock</legend><div className="form-grid">
      <div><label htmlFor="transfer-item">Item</label><select id="transfer-item" required value={item} onChange={e => setItem(e.target.value)}><option value="">Choose tracked item</option>{items.map(v => <option key={v.id} value={v.id}>{v.name}</option>)}</select></div>
      <div><label htmlFor="transfer-from">From</label><select id="transfer-from" required value={from} onChange={e => setFrom(e.target.value)}><option value="">Choose location</option>{locations.map(l => <option key={l.id} value={l.id}>{l.name}</option>)}</select></div>
      <div><label htmlFor="transfer-to">To</label><select id="transfer-to" required value={to} onChange={e => setTo(e.target.value)}><option value="">Choose location</option>{locations.map(l => <option key={l.id} value={l.id}>{l.name}</option>)}</select></div>
      <div><label htmlFor="transfer-qty">Quantity</label><input id="transfer-qty" type="number" required min={1} step={1} value={transferQty} onChange={e => setTransferQty(e.target.value)} /></div>
      <div><label htmlFor="transfer-date">Date</label><DateInput id="transfer-date" required value={date} onChange={e => setDate(e.target.value)}/></div>
      <div><label htmlFor="transfer-note">Note <span className="field-optional">Optional</span></label><input id="transfer-note" maxLength={500} value={note} onChange={e => setNote(e.target.value)} /></div>
    </div><p className="form-actions"><button disabled={busy || !item || !from || !to || from === to || !transferQty}>Transfer stock</button></p></fieldset></form>
    <h2>Recent adjustments</h2>
    <div className="table-scroll"><table className="doc-table"><caption>Latest 200 stock adjustments</caption><thead><tr><th>Date</th><th>Item</th><th>Change</th><th>Unit cost</th><th>Reason</th><th>Source</th></tr></thead><tbody>
      {history.map(a => <tr key={a.id}><td>{a.adjustment_date}</td><th scope="row">{a.item_name}</th><td>{a.qty_delta > 0 ? `+${a.qty_delta}` : a.qty_delta}</td><td>{formatCents(a.unit_cost_cents)}</td><td>{a.reason}</td><td>{a.stocktake_id ? "Stocktake" : "Adjustment"}</td></tr>)}
      {!history.length && <tr><td colSpan={6}>No adjustments yet.</td></tr>}
    </tbody></table></div>
  </section>;
}
