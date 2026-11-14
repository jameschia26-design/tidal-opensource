import { api } from "@/lib/api";
import type { LookupMaps } from "@/lib/imports";

type Contact = { id: number; name: string };
type Account = { id: number; code: string; name: string };
type Invoice = { id: number; number: string | null };

// A name or code that matches two records is dropped, so a row using it is refused rather than guessed.
function unique<T>(items: T[], key: (t: T) => string | null, value: (t: T) => number): Record<string, number> {
  const seen = new Map<string, number[]>();
  for (const it of items) {
    const k = key(it);
    if (k) seen.set(k, [...(seen.get(k) ?? []), value(it)]);
  }
  return Object.fromEntries([...seen].filter(([, v]) => v.length === 1).map(([k, v]) => [k, v[0]]));
}

export async function loadLookups(orgId: number, withInvoices: boolean): Promise<LookupMaps> {
  const [cust, supp, a, inv] = await Promise.all([
    api<{ contacts: Contact[] }>(`/v1/contacts?org_id=${orgId}&role=customer`),
    api<{ contacts: Contact[] }>(`/v1/contacts?org_id=${orgId}&role=supplier`),
    api<{ accounts: Account[] }>(`/v1/accounts?org_id=${orgId}`),
    withInvoices ? api<{ invoices: Invoice[] }>(`/v1/invoices?org_id=${orgId}&status=posted`) : Promise.resolve(null),
  ]);
  // Accounts resolve by code or by name: QuickBooks IIF writes the account name, MYOB and
  // Sage write the number. A duplicate on either key is dropped, so a row using it is refused.
  const byCode = unique(a.accounts, x => x.code.trim() || null, x => x.id);
  const byName = unique(a.accounts, x => x.name.trim().toLowerCase() || null, x => x.id);
  return {
    contacts: unique(cust.contacts, x => x.name.trim().toLowerCase() || null, x => x.id),
    suppliers: unique(supp.contacts, x => x.name.trim().toLowerCase() || null, x => x.id),
    accounts: { ...byName, ...byCode },
    invoices: inv ? unique(inv.invoices, x => x.number, x => x.id) : undefined,
  };
}
