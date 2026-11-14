"use client";
import { useEffect, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import Attachments from "@/components/Attachments";
import Extras from "@/components/Extras";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";
import Link from "next/link";
import { formatCents, parseCents } from "@/lib/money";
import { csvCell, downloadCsv } from "@/lib/csv";
import StockPanel from "@/components/StockPanel";

const blank = () => ({ name: "", item_type: "goods", unit: "", sku: "", sales_account_id: 0, purchase_account_id: 0, sales_description: "", purchase_description: "", preferred_vendor_id: 0, track_inventory: false, inventory_asset_account_id: 0, cogs_account_id: 0, archived: false });
type Item = ReturnType<typeof blank> & { id: number; selling_price_cents: number; cost_price_cents: number };
type Account = { id: number; name: string; type: string };
export default function ItemsPage() {
  const { selected } = useOrg();
  return <ItemsWorkspace key={selected?.id ?? "none"} />;
}
function ItemsWorkspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const [rows, setRows] = useState<Item[]>([]);
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [vendors, setVendors] = useState<{ id: number; name: string }[]>([]);
  const [form, setForm] = useState(blank);
  const [sale, setSale] = useState("0.00");
  const [cost, setCost] = useState("0.00");
  const [editing, setEditing] = useState<number | null>(null);
  const [open, setOpen] = useState(false);
  const [duplicateNext, setDuplicateNext] = useState(false);
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [statusFilter, setStatusFilter] = useState("active");
  const [onHand, setOnHand] = useState<Record<number, number>>({});
  useEffect(() => {
    if (!orgId) return;
    let live = true;
    Promise.all([api<{ items: Item[] }>(`/v1/items?org_id=${orgId}`), api<{ accounts: Account[] }>(`/v1/accounts?org_id=${orgId}`), api<{ contacts: { id: number; name: string }[] }>(`/v1/contacts?role=supplier&org_id=${orgId}`)])
      .then(([items, ledger, suppliers]) => { if (live) { setRows(items.items); setAccounts(ledger.accounts); setVendors(suppliers.contacts); } })
      .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); })
      .finally(() => { if (live) setLoading(false); });
    return () => { live = false; };
  }, [orgId]);
  useEffect(() => {
    if (!orgId) return;
    let live = true;
    // Quantity on hand comes from the stock ledger the inventory reports read, so the item list and
    // those reports cannot disagree.
    api<{ rows: { item_id: number; qty: number }[] }>(`/v1/reports/inventory-items?org_id=${orgId}&as_of=${new Date().toISOString().slice(0, 10)}`)
      .then(r => { if (live) setOnHand(Object.fromEntries(r.rows.map(v => [v.item_id, v.qty]))); })
      .catch(() => { /* the list still works without stock figures */ });
    return () => { live = false; };
  }, [orgId]);
  async function save(e: FormEvent) {
    e.preventDefault();
    if (busy || !orgId) return;
    const selling = parseCents(sale), purchase = parseCents(cost);
    if (selling === null || purchase === null) { setError("Enter prices with up to two decimal places."); return; }
    setBusy(true); setError("");
    try {
      const item = await api<Item>(editing ? `/v1/items/${editing}` : "/v1/items", { method: editing ? "PUT" : "POST", body: JSON.stringify({ ...form, org_id: orgId, selling_price_cents: selling, cost_price_cents: purchase }) });
      setRows(prev => [...prev.filter(v => v.id !== item.id), item].sort((a, b) => a.name.localeCompare(b.name)));
      if (duplicateNext) { setDuplicateNext(false); setEditing(null); setForm(current => ({ ...current, sku: "" })); setOpen(true); } else setOpen(false);
    } catch (e) { const err = e as ApiError; setError(err.error === "sku_exists" ? "This SKU already exists in these books." : messageFor(err.error, err.status)); }
    finally { setBusy(false); }
  }
  const writable = selected && selected.role !== "viewer";
  const text = (key: "name" | "unit" | "sku", label: string, limit: number) => <div><label htmlFor={`item-${key}`}>{label}</label><input id={`item-${key}`} required={key === "name"} maxLength={limit} value={form[key]} onChange={e => setForm({ ...form, [key]: e.target.value })} /></div>;
  const account = (key: "sales_account_id" | "purchase_account_id" | "inventory_asset_account_id" | "cogs_account_id", label: string, type: string) => <div><label htmlFor={`item-${key}`}>{label}</label><select id={`item-${key}`} value={form[key]} onChange={e => setForm({ ...form, [key]: Number(e.target.value) })}><option value={0}>{type === "revenue" ? "Sales (default)" : type === "asset" ? "Inventory asset (default)" : type === "expense" && key === "cogs_account_id" ? "Cost of goods sold (default)" : "Select purchase account"}</option>{accounts.filter(a => a.type === type).map(a => <option key={a.id} value={a.id}>{a.name}</option>)}</select></div>;
  const shown = rows.filter(v => (statusFilter === "all" || (statusFilter === "archived" ? v.archived : !v.archived)) && `${v.name} ${v.sku}`.toLowerCase().includes(query.toLowerCase()));
  const exportCsv = () => downloadCsv("items.csv", [["Name", "Type", "SKU", "Quantity", "Selling price", "Cost price", "Status"], ...shown.map(v => [v.name, v.item_type, v.sku, v.track_inventory && v.item_type === "goods" ? String(onHand[v.id] ?? 0) : "", (v.selling_price_cents / 100).toFixed(2), (v.cost_price_cents / 100).toFixed(2), v.archived ? "Archived" : "Active"])].map(r => r.map(csvCell).join(",")).join("\n") + "\n");
  return <><PageHeader title="Items" description="Reusable goods and services for sales and purchases." actions={<><Link className="button-secondary" href="/import">Import</Link>{rows.length > 0 && <button type="button" className="button-secondary" onClick={exportCsv}>Export</button>}</>} />
    {!selected ? <p className="notice">Select or create a company first.</p> : <>
      {error && <p role="alert" className="form-error">{error}</p>}
      <div className="record-toolbar"><div><label htmlFor="item-search">Search items</label><input id="item-search" type="search" value={query} onChange={e => setQuery(e.target.value)} /></div><label>Status<select id="item-status" value={statusFilter} onChange={e => setStatusFilter(e.target.value)}><option value="active">Active</option><option value="archived">Archived</option><option value="all">All</option></select></label>{writable && <button disabled={busy || loading} onClick={() => { setForm(blank()); setSale("0.00"); setCost("0.00"); setEditing(null); setOpen(true); }}>New item</button>}</div>
      {open && <form className="record-form" onSubmit={save}><fieldset disabled={busy}><legend>Item identity</legend><div className="form-grid">{text("name", "Name", 200)}<div><label htmlFor="item-type">Type</label><select id="item-type" value={form.item_type} onChange={e => setForm({ ...form, item_type: e.target.value })}><option value="goods">Goods</option><option value="service">Service</option></select></div>{text("unit", "Unit", 40)}{text("sku", "SKU", 100)}</div>
        <label className="check-label"><input type="checkbox" disabled={form.item_type !== "goods"} checked={form.track_inventory && form.item_type === "goods"} onChange={e => setForm({ ...form, track_inventory: e.target.checked })} />Track inventory</label><p className="section-help">Tracked goods count stock in and out when bills and invoices are posted. Services cannot be tracked.</p>
      </fieldset><fieldset disabled={busy}><legend>Sales</legend><div className="form-grid"><div><label htmlFor="item-sale">Selling price (SGD)</label><input id="item-sale" required inputMode="decimal" value={sale} onChange={e => setSale(e.target.value)} /></div>{account("sales_account_id", "Sales account", "revenue")}</div><label htmlFor="item-sales-description">Sales description</label><textarea id="item-sales-description" maxLength={4000} value={form.sales_description} onChange={e => setForm({ ...form, sales_description: e.target.value })} /></fieldset>
        <fieldset disabled={busy}><legend>Purchases</legend><div className="form-grid"><div><label htmlFor="item-cost">Cost price (SGD)</label><input id="item-cost" required inputMode="decimal" value={cost} onChange={e => setCost(e.target.value)} /></div>{account("purchase_account_id", "Purchase account", "expense")}{account("inventory_asset_account_id", "Inventory asset account", "asset")}{account("cogs_account_id", "Cost of goods sold account", "expense")}<div><label htmlFor="item-vendor">Preferred vendor</label><select id="item-vendor" value={form.preferred_vendor_id} onChange={e => setForm({ ...form, preferred_vendor_id: Number(e.target.value) })}><option value={0}>No preference</option>{vendors.map(v => <option key={v.id} value={v.id}>{v.name}</option>)}</select></div></div><label htmlFor="item-purchase-description">Purchase description</label><textarea id="item-purchase-description" maxLength={4000} value={form.purchase_description} onChange={e => setForm({ ...form, purchase_description: e.target.value })} /></fieldset>
        <fieldset><legend>Images</legend><input type="file" accept="image/*" disabled aria-label="Item images" /><p className="section-help">Image upload is not yet available.</p></fieldset>
        <div className="form-actions"><button type="submit" disabled={busy} onClick={() => setDuplicateNext(false)}>Save item</button><button type="submit" formNoValidate={false} disabled={busy} onClick={() => setDuplicateNext(true)}>Save &amp; duplicate</button><button disabled={busy} type="button" className="button-secondary" onClick={() => setOpen(false)}>Cancel</button></div>
      </form>}
      {open && editing != null && orgId != null && <><Attachments key={`a${editing}`} kind="items" id={editing} orgId={orgId} /><p className="section-help">Item images: upload picture files here. They are stored and downloadable, but not shown as thumbnails.</p><Extras key={editing} kind="items" id={editing} orgId={orgId} /></>}
      {orgId != null && rows.some(v => v.track_inventory && v.item_type === "goods") && <StockPanel orgId={orgId} items={rows.filter(v => v.track_inventory && v.item_type === "goods").map(v => ({ id: v.id, name: v.name, cost_price_cents: v.cost_price_cents }))} />}
      {loading ? <p role="status">Loading items…</p> : rows.length === 0 ? <div className="empty-state" role="status"><p>No items yet.</p><p>Add a good or service once and reuse it on invoices and bills.</p></div> : <div className="table-scroll"><table className="doc-table"><caption>Items (up to 500)</caption><thead><tr><th scope="col">Name</th><th scope="col">Type</th><th scope="col">SKU</th><th scope="col" className="num">Quantity</th><th scope="col" className="num">Selling price</th><th scope="col" className="num">Cost price</th><th scope="col">Status</th><th scope="col">Action</th></tr></thead><tbody>{shown.map(v => <tr key={v.id}><th scope="row">{v.name}</th><td>{v.item_type}</td><td>{v.sku || "—"}</td><td className="num">{v.track_inventory && v.item_type === "goods" ? (onHand[v.id] ?? 0) : "—"}</td><td className="num">{formatCents(v.selling_price_cents)}</td><td className="num">{formatCents(v.cost_price_cents)}</td><td>{v.archived ? "Archived" : "Active"}</td><td>{writable && <button className="button-secondary" disabled={busy} onClick={() => { setForm({ name: v.name, item_type: v.item_type, unit: v.unit, sku: v.sku, sales_account_id: v.sales_account_id, purchase_account_id: v.purchase_account_id, sales_description: v.sales_description, purchase_description: v.purchase_description, preferred_vendor_id: v.preferred_vendor_id, track_inventory: v.track_inventory, inventory_asset_account_id: v.inventory_asset_account_id ?? 0, cogs_account_id: v.cogs_account_id ?? 0, archived: v.archived }); setSale((v.selling_price_cents / 100).toFixed(2)); setCost((v.cost_price_cents / 100).toFixed(2)); setEditing(v.id); setOpen(true); }}>Edit<span className="sr-only"> {v.name}</span></button>}</td></tr>)}</tbody></table></div>}
      {rows.length > 0 && <p className="section-help" role="status">Showing {shown.length} of {rows.length} items.</p>}
    </>}
  </>;
}
