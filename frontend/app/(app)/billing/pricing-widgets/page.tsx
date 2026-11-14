"use client";
import { useEffect, useState, type FormEvent } from "react";
import PageHeader from "@/components/PageHeader";
import { useOrg } from "@/components/OrgProvider";
import { api, type ApiError } from "@/lib/api";
import { messageFor } from "@/lib/errors";

type Product = { id: number; name: string };
type Widget = { id: number; name: string; product: string };
export default function PricingWidgets() {
  const { selected } = useOrg();
  return <Workspace key={`${selected?.id ?? "none"}-${selected?.role ?? "none"}`} />;
}
function Workspace() {
  const { selected } = useOrg();
  const orgId = selected?.id;
  const canWrite = selected != null && selected.role !== "viewer";
  const [products, setProducts] = useState<Product[]>([]);
  const [widgets, setWidgets] = useState<Widget[] | null>(null);
  const [name, setName] = useState("");
  const [product, setProduct] = useState("");
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    if (orgId == null) return;
    let live = true;
    Promise.all([api<{ rows: Product[] }>(`/v1/billing/products?org_id=${orgId}`), api<{ widgets: Widget[] }>(`/v1/billing/pricing-widgets?org_id=${orgId}`)])
      .then(([p, w]) => { if (live) { setProducts(p.rows); setWidgets(w.widgets); } })
      .catch((e: ApiError) => { if (live) setError(messageFor(e.error, e.status)); });
    return () => { live = false; };
  }, [orgId]);
  async function save(e: FormEvent) {
    e.preventDefault();
    if (!orgId || busy || !name.trim() || !product) return;
    setBusy(true); setError("");
    try {
      await api("/v1/billing/pricing-widgets", { method: "POST", body: JSON.stringify({ org_id: orgId, name: name.trim(), product_id: Number(product) }) });
      const result = await api<{ widgets: Widget[] }>(`/v1/billing/pricing-widgets?org_id=${orgId}`);
      setWidgets(result.widgets); setOpen(false); setName(""); setProduct("");
    } catch (e) { const x = e as ApiError; setError(messageFor(x.error, x.status)); }
    finally { setBusy(false); }
  }
  return <><PageHeader title="Pricing widgets" description="Set up a widget name and product." />
    <p className="notice">Setup saves the name and product only. Styling, publishing, embedding and checkout are not available yet.</p>
    {!selected && <p>Choose a company first.</p>}{error && <p role="alert" className="form-error">{error}</p>}
    {canWrite && !open && <button onClick={() => setOpen(true)}>New widget</button>}
    {open && canWrite && <form className="record-form" onSubmit={save}><fieldset disabled={busy}><legend>New pricing widget</legend><div className="form-grid"><div><label htmlFor="widget-name">Name</label><input id="widget-name" required maxLength={100} value={name} onChange={e => setName(e.target.value)} /></div><div><label htmlFor="widget-product">Product</label><select id="widget-product" required value={product} onChange={e => setProduct(e.target.value)}><option value="">Choose product</option>{products.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}</select></div></div><div className="form-actions"><button>Save setup</button><button type="button" className="button-secondary" onClick={() => setOpen(false)}>Cancel</button></div></fieldset></form>}
    {widgets?.length === 0 && <p>No pricing widgets yet.</p>}{!!widgets?.length && <div className="table-scroll"><table className="doc-table"><caption>Pricing widget setups</caption><thead><tr><th>Name</th><th>Product</th></tr></thead><tbody>{widgets.map(w => <tr key={w.id}><td>{w.name}</td><td>{w.product}</td></tr>)}</tbody></table></div>}
  </>;
}
