import type { ImportSpec } from "@/components/RecordImport";

// Money from a CSV cell: "12", "12.5" or "12.50" only. Anything else returns null and the row is refused.
const cents = (v: string): number | null => {
  const t = v.trim();
  if (!/^\d+(\.\d{1,2})?$/.test(t) || Number(t) <= 0) return null;
  return Math.round(Number(t) * 100);
};
const whole = (v: string): number | null => (/^\d+$/.test(v.trim()) ? Number(v.trim()) : null);
const address = () => ({ attention: "", line1: "", line2: "", city: "Singapore", region: "", postal_code: "", country: "SG", phone: "", fax: "" });
const isoDay = (v: string): string | null => (/^\d{4}-\d{2}-\d{2}$/.test(v.trim()) ? v.trim() : null);
// Idempotency key per import file and row: re-running the same file does not double-post, a new file is a new key.
const rowKey = (kind: string, fileTag: string, row: number) => `import-${kind}-${fileTag}-r${row}`;

export type LookupMaps = {
  contacts: Record<string, number>; // exact lower-case customer name -> id
  suppliers: Record<string, number>; // exact lower-case supplier name -> id
  accounts: Record<string, number>; // account code -> id
  invoices?: Record<string, number>; // invoice number -> id
};

// Each import mirrors the create form's required fields. Customers, accounts and invoices are matched exactly;
// a row that does not match is refused with its reason, never guessed.
export function importSpecs(lookups: LookupMaps): Record<string, ImportSpec> {
  const contact = (name: string) => lookups.contacts[name.trim().toLowerCase()];
  const supplier = (name: string) => (lookups.suppliers ?? {})[name.trim().toLowerCase()];
  // Accounts resolve by code or by name: vendor exports use one or the other.
  const account = (key: string) => lookups.accounts[key.trim()] ?? lookups.accounts[key.trim().toLowerCase()];
  const lineOf = (r: Record<string, string>) => {
    const q = whole(r.quantity ?? ""), p = cents(r.unit_price ?? "");
    if (q == null || q < 1 || p == null) return "quantity must be a whole number of at least 1 and unit_price a positive amount";
    return { q, p };
  };
  return {
    accounts: {
      label: "accounts", endpoint: "/v1/accounts", headers: ["code", "name", "type"],
      help: "type is asset, liability, equity, revenue or expense; class is optional and defaults from the type and code. Codes 1000/1100/2100/4000 are reserved for posting rules and are refused.",
      toBody: (r) => ({
        code: r.code ?? "", name: r.name ?? "", type: r.type ?? "", class: r.class ?? "", description: r.description ?? "",
        show_on_dashboard: false, show_in_expense_claims: false, enable_payments: false,
      }),
    },
    customers: {
      label: "customers", endpoint: "/v1/contacts", headers: ["name"],
      help: "Optional: email, company_name, contact_type (business or individual), phone, payment_terms_days (0 to 365).",
      toBody: (r, _row, fileTag) => {
        const terms = r.payment_terms_days ? whole(r.payment_terms_days) : 0;
        if (terms == null || terms > 365) return "payment_terms_days must be a whole number from 0 to 365";
        return { role: "customer", name: r.name ?? "", email: r.email ?? "", company_name: r.company_name ?? "", contact_type: r.contact_type || "business", phone: r.phone ?? "", payment_terms_days: terms,
          language: "en", currency: "SGD", portal_enabled: false, owner_user_id: 0, opening_balance_cents: 0, billing_address: address(), shipping_address: address(), contact_persons: [], notes: "", internal_remarks: "" };
      },
    },
    vendors: {
      label: "suppliers", endpoint: "/v1/contacts", headers: ["name"],
      help: "Same fields as customers; the role is forced to supplier.",
      toBody: (r) => ({
        role: "supplier", name: r.name ?? "", email: r.email ?? "", company_name: r.company_name ?? "", contact_type: r.contact_type || "business", phone: r.phone ?? "", payment_terms_days: whole(r.payment_terms_days ?? "0") ?? 0,
        language: "en", currency: "SGD", portal_enabled: false, owner_user_id: 0, opening_balance_cents: 0, billing_address: address(), shipping_address: address(), contact_persons: [], notes: "", internal_remarks: "",
      }),
    },
    items: {
      label: "items", endpoint: "/v1/items", headers: ["name", "item_type"],
      help: "item_type is goods or service. selling_price is a plain amount; the sales account is optional.",
      toBody: (r) => {
        // A zero price is legitimate (a free or not-yet-priced item); only unreadable text is refused.
        const raw = (r.selling_price ?? "").trim();
        const price = raw === "" ? 0 : /^\d+(\.\d{1,2})?$/.test(raw) ? Math.round(Number(raw) * 100) : null;
        if (price == null) return "selling_price must be an amount like 0 or 150.00";
        return { name: r.name ?? "", item_type: r.item_type || "service", sku: r.sku ?? "", unit: "", selling_price_cents: price, cost_price_cents: 0,
          sales_account_id: account(r.sales_account_code ?? "") ?? 0, purchase_account_id: account(r.purchase_account_code ?? "") ?? 0,
          sales_description: r.description ?? "", purchase_description: "", preferred_vendor_id: 0, track_inventory: (r.item_type ?? "") === "goods" };
      },
    },
    bills: {
      label: "bills", endpoint: "/v1/bills", headers: ["vendor", "bill_date", "due_date", "description", "quantity", "unit_price", "account_code"],
      help: "One row per bill, one line each. Supplier matched by exact name; expense account by code. Bills are created as drafts.",
      toBody: (r) => {
        const sid = supplier(r.vendor ?? ""), date = isoDay(r.bill_date ?? ""), due = isoDay(r.due_date ?? "");
        const acct = account(r.account_code ?? "");
        if (!sid) return `supplier "${r.vendor}" not found`;
        if (!date) return "bill_date must be YYYY-MM-DD";
        if (!acct) return `expense account "${r.account_code}" not found`;
        const line = lineOf(r); if (typeof line === "string") return line;
        return { contact_id: sid, supplier_reference: r.bill_number ?? "", bill_date: date, due_date: due || date, notes: "", order_number: "", subject: "", payment_terms_days: 0,
          lines: [{ account_id: acct, description: r.description ?? "", quantity: line.q, unit_price_cents: line.p }] };
      },
    },
    invoices: {
      label: "invoices", endpoint: "/v1/invoices", headers: ["customer", "invoice_date", "due_date", "description", "quantity", "unit_price"],
      help: "One row per invoice, one line each. Customer matched by exact name. Dates YYYY-MM-DD. Optional: reference.",
      toBody: (r, _row, fileTag) => {
        const cid = contact(r.customer ?? ""), idate = isoDay(r.invoice_date ?? ""), ddate = isoDay(r.due_date ?? "");
        if (!cid) return `customer "${r.customer}" not found`;
        if (!idate || !ddate || ddate < idate) return "invoice_date and due_date must be YYYY-MM-DD, due on or after invoice date";
        const line = lineOf(r); if (typeof line === "string") return line;
        return { contact_id: cid, invoice_date: idate, due_date: ddate, reference: r.reference ?? "", notes: "", terms: "", lines: [{ description: r.description ?? "", quantity: line.q, unit_price_cents: line.p }] };
      },
    },
    quotes: {
      label: "quotes", endpoint: "/v1/quotes", headers: ["customer", "quote_date", "expiry_date", "description", "quantity", "unit_price"],
      help: "One row per quote, one line each. Customer matched by exact name. Dates YYYY-MM-DD; expiry on or after quote date.",
      toBody: (r, _row, fileTag) => {
        const cid = contact(r.customer ?? ""), qd = isoDay(r.quote_date ?? ""), ed = isoDay(r.expiry_date ?? "");
        if (!cid) return `customer "${r.customer}" not found`;
        if (!qd || !ed || ed < qd) return "quote_date and expiry_date must be YYYY-MM-DD, expiry on or after quote date";
        const line = lineOf(r); if (typeof line === "string") return line;
        return { contact_id: cid, quote_date: qd, expiry_date: ed, reference: "", notes: "", terms: "", lines: [{ description: r.description ?? "", quantity: line.q, unit_price_cents: line.p }] };
      },
    },
    "sales receipts": {
      label: "sales receipts", endpoint: "/v1/sales-receipts", headers: ["customer", "receipt_date", "description", "quantity", "unit_price", "deposit_account_code", "income_account_code"],
      help: "One receipt per row, one line each. Customer by exact name; deposit and income accounts by code.",
      toBody: (r, row, fileTag) => {
        const cid = contact(r.customer ?? ""), rd = isoDay(r.receipt_date ?? "");
        const dep = account(r.deposit_account_code ?? ""), inc = account(r.income_account_code ?? "");
        if (!cid) return `customer "${r.customer}" not found`;
        if (!rd) return "receipt_date must be YYYY-MM-DD";
        if (!dep) return `deposit account "${r.deposit_account_code}" not found`;
        if (!inc) return `income account "${r.income_account_code}" not found`;
        const line = lineOf(r); if (typeof line === "string") return line;
        return { contact_id: cid, receipt_date: rd, payment_mode: "bank_transfer", deposit_account_id: dep, reference: "", notes: "", terms: "",
          idempotency_key: rowKey("receipt", fileTag, row), lines: [{ description: r.description ?? "", account_id: inc, quantity: line.q, unit_price_cents: line.p }] };
      },
    },
    "credit notes": {
      label: "credit notes", endpoint: "/v1/credit-notes", headers: ["customer", "credit_date", "description", "quantity", "unit_price", "income_account_code"],
      help: "One credit per row, one line each. Imported credits are drafts; open them from the credit note page.",
      toBody: (r, _row, fileTag) => {
        const cid = contact(r.customer ?? ""), cd = isoDay(r.credit_date ?? ""), inc = account(r.income_account_code ?? "");
        if (!cid) return `customer "${r.customer}" not found`;
        if (!cd) return "credit_date must be YYYY-MM-DD";
        if (!inc) return `income account "${r.income_account_code}" not found`;
        const line = lineOf(r); if (typeof line === "string") return line;
        return { contact_id: cid, credit_date: cd, reference: "", notes: "", terms: "", lines: [{ description: r.description ?? "", account_id: inc, quantity: line.q, unit_price_cents: line.p }] };
      },
    },
    payments: {
      label: "payments", endpoint: "/v1/payments-received", headers: ["customer", "payment_date", "amount", "deposit_account_code", "invoice_number"],
      help: "One payment per row, applied in full to the named invoice. Invoice must belong to that customer and have enough balance; the server checks this.",
      toBody: (r, row, fileTag) => {
        const cid = contact(r.customer ?? ""), pd = isoDay(r.payment_date ?? ""), amt = cents(r.amount ?? ""), dep = account(r.deposit_account_code ?? "");
        if (!cid) return `customer "${r.customer}" not found`;
        if (!pd) return "payment_date must be YYYY-MM-DD";
        if (amt == null) return "amount must be a positive amount";
        if (!dep) return `deposit account "${r.deposit_account_code}" not found`;
        const inv = lookups.invoices?.[(r.invoice_number ?? "").trim()];
        if (!inv) return `invoice "${r.invoice_number}" not found`;
        return { contact_id: cid, payment_date: pd, payment_mode: "bank_transfer", payment_number: "", account_id: dep, amount_cents: amt, bank_charge_cents: 0, bank_charge_account_id: 0, reference: "", notes: "", idempotency_key: rowKey("payment", fileTag, row), allocations: [{ id: inv, amount_cents: amt }] };
      },
    },
    expenses: {
      label: "expenses", endpoint: "/v1/expenses", headers: ["expense_date", "payee", "description", "amount", "expense_account_code"],
      help: "One expense per row. Expense account by code. Amount in SGD. Not billable; no GST.",
      toBody: (r, row, fileTag) => {
        const d = isoDay(r.expense_date ?? ""), amt = cents(r.amount ?? ""), acct = account(r.expense_account_code ?? "");
        if (!d) return "expense_date must be YYYY-MM-DD";
        if (!r.payee?.trim() || !r.description?.trim()) return "payee and description are required";
        if (amt == null) return "amount must be a positive amount";
        if (!acct) return `expense account "${r.expense_account_code}" not found`;
        return { expense_date: d, payee: r.payee.trim(), description: r.description.trim(), expense_account_id: acct, amount_cents: amt, idempotency_key: rowKey("expense", fileTag, row) };
      },
    },
    "recurring expenses": {
      label: "recurring expenses", endpoint: "/v1/recurring-expenses", headers: ["name", "payee", "description", "amount", "expense_account_code", "paid_through_account_code", "start_date", "frequency"],
      help: "One profile per row. frequency must be monthly. Both account columns use account codes. Amount in SGD. Nothing posts until you press Run due expenses.",
      toBody: (r) => {
        const sd = isoDay(r.start_date ?? ""), amt = cents(r.amount ?? ""), acct = account(r.expense_account_code ?? ""), paid = account(r.paid_through_account_code ?? "");
        if (!r.name?.trim()) return "name is required";
        if (!sd) return "start_date must be YYYY-MM-DD";
        if (!r.payee?.trim() || !r.description?.trim()) return "payee and description are required";
        if (amt == null) return "amount must be a positive amount";
        if (!acct) return `expense account "${r.expense_account_code}" not found`;
        if (!paid) return `paid-through account "${r.paid_through_account_code}" not found`;
        if ((r.frequency ?? "").trim().toLowerCase() !== "monthly") return "frequency must be monthly";
        return { name: r.name.trim(), repeat_every: 1, repeat_unit: "month", start_date: sd, ends_on: "", expense_account_id: acct, paid_through_account_id: paid, customer_id: 0, payee: r.payee.trim(), amount_cents: amt, notes: "" };
      },
    },
  };
}
