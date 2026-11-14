// Migration import: read a file exported by another accounting system, work out what it is,
// and rewrite it into the column set this app already imports. Parsing is deterministic —
// the same file always produces the same rows, and every rejected row says why.
// Mappings stay explicit: quietly guessing a column can put a wrong number in the ledger.

export type Source = "xero" | "quickbooks" | "quickbooks-desktop" | "myob" | "sage" | "peachtree";
export type Entity = "accounts" | "customers" | "vendors" | "items" | "invoices" | "bills";

export type Table = { source: Source; entity: Entity; headers: string[]; rows: Record<string, string>[] };

const norm = (s: string) => s.toLowerCase().replace(/[^a-z0-9]/g, "");

// ---------------------------------------------------------------- file parsing

/** Comma or tab, quoted cells, CRLF, optional BOM. QuickBooks desktop uses tabs; everything else commas. */
export function parseTable(text: string): { headers: string[]; rows: Record<string, string>[] } {
  const body = text.replace(/^\uFEFF/, "");
  const delim = body.split("\n")[0].includes("\t") ? "\t" : ",";
  const records: string[][] = [];
  let row: string[] = [];
  let cell = "";
  let quoted = false;
  for (let i = 0; i < body.length; i++) {
    const c = body[i];
    if (quoted) {
      if (c === '"' && body[i + 1] === '"') { cell += '"'; i++; }
      else if (c === '"') quoted = false;
      else cell += c;
    } else if (c === '"') quoted = true;
    else if (c === delim) { row.push(cell); cell = ""; }
    else if (c === "\n" || c === "\r") {
      if (c === "\r" && body[i + 1] === "\n") i++;
      row.push(cell);
      if (row.some((v) => v.trim() !== "")) records.push(row);
      row = []; cell = "";
    } else cell += c;
  }
  row.push(cell);
  if (row.some((v) => v.trim() !== "")) records.push(row);
  if (records.length < 2) throw new Error("The file needs a header row and at least one data row.");
  const headers = records[0].map((h) => h.trim());
  const rows = records.slice(1).map((r) => Object.fromEntries(headers.map((h, i) => [h, r[i] ?? ""])));
  return { headers, rows };
}

/** QuickBooks desktop IIF: `!TYPE` lines declare columns, body lines carry values by position. */
export function parseIIF(text: string): { headers: string[]; rows: Record<string, string>[] } {
  const decl: Record<string, string[]> = {};
  const rows: Record<string, string>[] = [];
  for (const raw of text.replace(/^\uFEFF/, "").split(/\r\n|\n/)) {
    const cells = raw.split("\t");
    const tag = cells[0];
    if (tag.startsWith("!")) { decl[tag.slice(1)] = cells.slice(1); continue; }
    const cols = decl[tag];
    if (!cols) continue;
    // _tag remembers the record type: one IIF file can hold several (ACCNT, CUST, TRNS...).
    rows.push({ _tag: tag, ...Object.fromEntries(cols.map((h, i) => [h, cells[i + 1] ?? ""])) });
  }
  if (!rows.length) throw new Error("No IIF rows found.");
  const headers = [...new Set(rows.flatMap((r) => Object.keys(r).filter((k) => k !== "_tag")))];
  return { headers, rows };
}

// ---------------------------------------------------------------- detection

const SIGNATURES: Record<Source, string[]> = {
  "quickbooks-desktop": ["accnt", "cust", "vend", "trns"],
  peachtree: ["accountid", "accountdescription", "customerid", "itemid", "glsalesaccount", "glpurchaseaccount", "glaccount", "invoicecm"],
  quickbooks: ["fullname", "detailtype", "openingbalance", "salesprice"],
  // MYOB is the only one of these that writes its tax code into the account/card columns.
  myob: ["colastname", "cardid", "itemnumber", "gstcode", "gstcodenz", "taxcodeau", "taxcoden", "lastchequenumber", "header"],
  // "ContactName" is shared by multiple sources, so Sage claims only columns
  // Peachtree never writes: CountryCode, CustomerName/AccountName spellings, Supplier IDs.
  sage: ["countrycode", "customername", "accountname", "supplierid"],
  xero: ["contactname", "emailaddress", "poaddressline1", "accountsreceivabletaxtype", "accountspayabletaxtype", "taxdefault", "itemcode"],
};

export function detectSource(headers: string[], text = ""): Source | null {
  const h = headers.map(norm);
  if (text.split("\n")[0].trimStart().startsWith("!")) return "quickbooks-desktop";
  let best: { source: Source; score: number } | null = null;
  for (const [source, sig] of Object.entries(SIGNATURES) as [Source, string[]][]) {
    if (source === "quickbooks-desktop") continue;
    const score = sig.filter((s) => h.includes(s)).length;
    if (score > 0 && (!best || score > best.score)) best = { source, score };
  }
  return best ? best.source : null;
}

const has = (h: string[], ...names: string[]) => names.every((n) => h.includes(norm(n)));
const any = (h: string[], ...names: string[]) => names.some((n) => h.includes(norm(n)));

/**
 * Ordered rules, most distinctive first. A line journal carries a party name too, so the
 * journal checks have to run before the contact-list checks or a sales journal imports as
 * a list of customers. When a file genuinely cannot be told apart (a MYOB card export is
 * identical for customers and suppliers) this returns the likelier guess and the caller
 * offers an override.
 */
export function detectEntity(headers: string[]): Entity | null {
  const h = headers.map(norm);
  // Journals first: a sales/purchase journal carries a party name too, so rules for
  // contact lists must not claim it. "Invoice #" alone means a journal only when a
  // vendor context is present; an invoice list uses "Invoice/CM #" or "Invoice Number".
  if (any(h, "Invoice/CM #", "InvoiceNumber", "Invoice No.", "Invoice No")) return "invoices";
  if (any(h, "Purchase No.", "Supplier Invoice No.", "SupplierInvoiceNo") || (has(h, "Invoice #") && any(h, "Vendor ID", "Vendor Name", "Vendor", "Supplier"))) return "bills";
  // A GL account list always carries either an account type or an account number in company
  // chart-of-accounts form (Account Number, Account # or AccountCode) — but a contact list
  // carries account numbers too (G/L Sales Account), so those need the chart headers.
  if (any(h, "Account Type", "Account ID", "Account Description", "TaxDefault") || has(h, "Account Number") && any(h, "Account Name", "Header")) return "accounts";
  if (any(h, "Vendor ID", "Vendor Name", "Supplier ID", "Supplier Name")) return "vendors";
  if (any(h, "Customer ID", "Customer Name", "Co./Last Name", "Full Name", "ContactName")) return "customers";
  if (any(h, "Item ID", "ItemCode", "Item Number", "Item Name", "SKU", "Item Description")) return "items";
  return null;
}

// ---------------------------------------------------------------- value normalisation

const pad = (n: number) => String(n).padStart(2, "0");

/** Dates arrive as M/D/YYYY, D/M/YYYY, DD-MM-YY or ISO. US systems write month first. */
export function toISODate(raw: string, source: Source): string | null {
  const v = raw.trim().replace(/^"|"$/g, "");
  if (!v) return null;
  const iso = /^(\d{4})-(\d{2})-(\d{2})/.exec(v);
  if (iso) return `${iso[1]}-${iso[2]}-${iso[3]}`;
  const m = /^(\d{1,2})[/.-](\d{1,2})[/.-](\d{2,4})$/.exec(v);
  if (!m) return null;
  const us = source === "quickbooks" || source === "quickbooks-desktop" || source === "peachtree";
  let month = Number(us ? m[1] : m[2]);
  let day = Number(us ? m[2] : m[1]);
  // A first number above 12 can only be a day, whatever the system claims.
  if (month > 12 && day <= 12) [month, day] = [day, month];
  let year = Number(m[3]);
  if (m[3].length === 2) year += year < 70 ? 2000 : 1900;
  if (month < 1 || month > 12 || day < 1 || day > 31) return null;
  return `${year}-${pad(month)}-${pad(day)}`;
}

/** "$1,234.50" / "(45.00)" / "-59.25" -> integer cents. Parentheses mean negative. */
export function toCents(raw: string): number | null {
  let v = raw.trim().replace(/^"|"$/g, "");
  if (!v) return null;
  let negative = false;
  if (/^\(.*\)$/.test(v)) { negative = true; v = v.slice(1, -1); }
  v = v.replace(/[^0-9.-]/g, "");
  if (v.startsWith("-")) { negative = !negative; v = v.slice(1); }
  if (!/^\d*\.?\d*$/.test(v) || v === "" || v === ".") return null;
  const cents = Math.round(Number(v) * 100);
  return Number.isFinite(cents) ? (negative ? -cents : cents) : null;
}

const ACCOUNT_TYPES: Record<string, string> = {
  // QuickBooks letter codes.
  bank: "asset", cc: "liability", ccard: "liability", ar: "asset", ap: "liability", ocliab: "liability",
  ltliab: "liability", equity: "equity", inc: "revenue", cogs: "expense", exp: "expense",
  exinc: "revenue", exexp: "expense", fixasset: "asset", oasset: "asset", ocasset: "asset", nonposting: "asset",
  // Sage 50 US / Peachtree numeric account types.
  "0": "asset", "1": "asset", "2": "asset", "3": "asset", "4": "asset", "5": "asset", "6": "asset",
  "8": "asset", "10": "liability", "11": "liability", "12": "liability", "14": "liability",
  "16": "equity", "18": "equity", "19": "equity", "21": "revenue", "23": "expense", "24": "expense",
  // Spelled-out types, which MYOB and Sage write in place of codes.
  asset: "asset", currentasset: "asset", fixedasset: "asset", otherasset: "asset", bankaccount: "asset",
  cash: "asset", accountsreceivable: "asset", inventory: "asset", stock: "asset",
  liability: "liability", currentliability: "liability", longtermliability: "liability",
  accountspayable: "liability", creditcard: "liability",
  shareholdersfunds: "equity", retainearnings: "equity",
  revenue: "revenue", income: "revenue", sales: "revenue", otherincome: "revenue",
  expense: "expense", expenses: "expense", costofsales: "expense", directcosts: "expense",
  otherexpense: "expense", overhead: "expense", purchases: "expense",
};

export function toAccountType(raw: string): string | null {
  const v = norm(raw);
  if (!v) return null;
  return ACCOUNT_TYPES[v] ?? ACCOUNT_TYPES[raw.trim()] ?? null;
}

// Standard class under the broad type. Same code-prefix rules as the backend's 120 backfill,
// so an imported chart lands on the classes an existing one was migrated to.
export function defaultAccountClass(type: string, code: string): string {
  if (type === "revenue") return code.startsWith("4") ? "sales" : "revenue";
  if (type === "expense") return code.startsWith("5") ? "cost_of_sales" : "expense";
  if (type === "asset") {
    if (code.startsWith("28")) return "bank";
    if (code.startsWith("15") || code.startsWith("17")) return "fixed_asset";
    if (code.startsWith("24")) return "inventory";
    return "current_asset";
  }
  if (type === "liability") return "current_liability";
  return "equity";
}

// ---------------------------------------------------------------- column mapping

// Every spelling we have seen for one Tidal field. First match wins; later ones are fallbacks.
const FIELD_ALIASES: Record<Entity, Record<string, string[]>> = ({
  accounts: {
    code: ["Account ID", "Account Number", "AccountCode", "Code", "AccNum", "RefNum", "Number"],
    name: ["Account Description", "Account Name", "AccountName", "Name"],
    type: ["Account Type", "AccountType", "Type", "AccntType"],
    description: ["Description", "Notes", "Memo"],
    // Opening balance column, when the export carries one. Debit-positive, as both vendors write it.
    balance: ["OBAMOUNT", "Opening Balance", "Balance", "Current Balance", "Begin Yr Net-Total", "Net-Total"],
  },
  customers: {
    name: ["ContactName", "Customer Name", "CustomerName", "Full Name", "Company Name", "Company", "Co./Last Name", "Name"],
    email: ["EmailAddress", "Customer Email", "Email", "Email Address", "Contact Email"],
    phone: ["PhoneNumber", "Telephone 1", "Phone", "Phone 1", "Contact Phone"],
    company_name: ["Company Name", "CompanyName"],
    payment_terms_days: ["Due Days", "Terms"],
  },
  vendors: {
    name: ["Vendor Name", "VendorName", "Supplier Name", "SupplierName", "ContactName", "Card Contact", "Name", "Company", "Co./Last Name"],
    email: ["Vendor Email", "EmailAddress", "Email"],
    phone: ["Telephone 1", "Phone", "PhoneNumber", "Card Phone", "Phone 1"],
    company_name: ["Company Name", "CompanyName"],
    payment_terms_days: ["Due Days", "Terms"],
    // A MYOB card file is identical for customers and suppliers; the supplier override
    // brings these in as suppliers without changing the customer import.
    address: ["Addr 1 - Line 1", "Address-Line 1", "Bill to Address-Line One"],
  },
  items: {
    name: ["Item Name", "Item Description", "ItemName", "Name", "Description"],
    sku: ["Item ID", "Item Number", "Item Code", "UPC/SKU", "SKU", "Code", "ItemCode"],
    selling_price: ["Selling Price", "Sales Price", "Sales Price/Billing Rate 1-10", "UnitPrice", "Unit Amount", "UnitAmount", "Price", "Rate"],
    item_type: ["Item Type", "InvItemType", "Inventory"],
    description: ["Sales Description", "Description", "Notes"],
  },
  invoices: {
    customer: ["ContactName", "Customer Name", "Customer", "CustomerName", "Full Name", "Co./Last Name", "Name"],
    invoice_number: ["InvoiceNumber", "Invoice/CM #", "Invoice No.", "Invoice No", "DocNum"],
    invoice_date: ["InvoiceDate", "Invoice Date", "Date"],
    due_date: ["Date Due", "DueDate", "Due Date"],
    description: ["Description", "Item Description", "Memo", "InvItem"],
    quantity: ["Quantity", "Qnty", "Qty"],
    unit_price: ["Unit Price", "Price", "UnitAmount", "UnitPrice", "Rate", "Amount"],
    account_code: ["G/L Account", "Accnt", "AccountCode", "Account No.", "Account Number"],
    reference: ["Customer PO", "Reference", "PO Number", "DocNum"],
  },
  bills: {
    vendor: ["Vendor Name", "Vendor", "Supplier", "VendorName", "ContactName", "Co./Last Name", "Name"],
    bill_number: ["Invoice #", "Purchase No.", "Bill Number", "Supplier Invoice No.", "SupplierInvoiceNo", "DocNum"],
    // A bill exported through an invoice-shaped file (Peachtree Sales/Purchase
    // journals both use the document date) still names the column like an invoice date.
    bill_date: ["Date", "Bill Date", "BillDate", "InvoiceDate", "Invoice Date"],
    due_date: ["Date Due", "DueDate", "Due Date"],
    description: ["Description", "Memo", "InvItem"],
    quantity: ["Quantity", "Qnty", "Qty"],
    unit_price: ["Unit Price", "Amount", "UnitAmount", "Price", "Rate"],
    account_code: ["G/L Account", "Accnt", "Account No.", "Account Number", "AccountCode"],
  },
});

export type Mapped = { rows: Record<string, string>[]; rejected: { row: number; reason: string }[]; unmapped: string[] };

/**
 * Rewrite vendor rows into the columns this app imports. A row that lacks a field the
 * target import requires is rejected with its reason — never filled with a guess.
 */
export function mapRows(table: Table): Mapped {
  const fields = FIELD_ALIASES[table.entity];
  const out: Record<string, string>[] = [];
  const rejected: { row: number; reason: string }[] = [];
  const used = new Set<string>();
  const byNorm = new Map(table.headers.map((h) => [norm(h), h]));
  for (const aliases of Object.values(fields)) {
    for (const a of aliases) {
      const k = byNorm.get(norm(a));
      if (k) { used.add(k); break; }
    }
  }

  table.rows.forEach((row, i) => {
    const n = i + 2; // header is line 1
    const rec: Record<string, string> = {};
    // First field to claim a column keeps it: "Account Description" is the name,
    // never also the description. Field order in FIELD_ALIASES is the priority order.
    const claimed = new Set<string>();
    const take = (aliases: string[]): string => {
      const byNorm = new Map<string, string>();
      for (const k of Object.keys(row)) if (!claimed.has(k)) byNorm.set(norm(k), k);
      for (const alias of aliases) {
        const key = byNorm.get(norm(alias));
        if (key !== undefined && String(row[key]).trim() !== "") { claimed.add(key); return String(row[key]).trim(); }
      }
      return "";
    };
    for (const [field, aliases] of Object.entries(fields)) rec[field] = take(aliases);

    if (table.entity === "accounts") {
      if (!rec.code) return rejected.push({ row: n, reason: "missing account code" });
      if (!rec.name) return rejected.push({ row: n, reason: `account ${rec.code} has no name` });
      const t = toAccountType(rec.type);
      if (!t) return rejected.push({ row: n, reason: `account ${rec.code} has an unknown type "${rec.type}"` });
      rec.type = t;
      rec.class = defaultAccountClass(t, rec.code);
      // A balance we cannot read is dropped with the row's other unused columns, never defaulted to zero.
      if (rec.balance) {
        const bal = toCents(rec.balance);
        if (bal == null) delete rec.balance;
        else rec.balance = (bal / 100).toFixed(2);
      } else delete rec.balance;
    } else if (table.entity === "customers" || table.entity === "vendors") {
      if (!rec.name) return rejected.push({ row: n, reason: "missing name" });
      const terms = rec.payment_terms_days ? Number(rec.payment_terms_days.replace(/\D/g, "")) : 0;
      rec.payment_terms_days = Number.isFinite(terms) && terms >= 0 && terms <= 365 ? String(terms) : "0";
      // Balance and address columns ride along for the review table but are not Tidal fields.
      delete (rec as Record<string, unknown> as { address?: unknown }).address;
    } else if (table.entity === "items") {
      if (!rec.name) return rejected.push({ row: n, reason: "missing item name" });
      const price = rec.selling_price ? toCents(rec.selling_price) : 0;
      if (price == null || price < 0) return rejected.push({ row: n, reason: `item ${rec.name} has an unreadable price` });
      rec.selling_price = (price / 100).toFixed(2);
      rec.item_type = /inventory|goods|stock/i.test(rec.item_type) ? "goods" : "service";
      rec.sku = rec.sku || rec.name.slice(0, 100);
    } else {
      if (row.TAXLINE === "1") return rejected.push({ row: n, reason: "sales tax line: Tidal recalculates GST per line, so this line is not imported" });
      // Line documents: the parent key and a date must be readable before anything else matters.
      const dateField = table.entity === "invoices" ? "invoice_date" : "bill_date";
      const party = table.entity === "invoices" ? rec.customer : rec.vendor;
      if (!party) return rejected.push({ row: n, reason: "missing customer or supplier" });
      const date = toISODate(rec[dateField] ?? "", table.source);
      if (!date) return rejected.push({ row: n, reason: `${dateField.replace("_", " ")} is not a date` });
      rec[dateField] = date;
      rec.due_date = toISODate(rec.due_date ?? "", table.source) ?? date;
      const qty = rec.quantity ? Number(rec.quantity.replace(/,/g, "")) : 1;
      if (!Number.isFinite(qty) || qty < 1) return rejected.push({ row: n, reason: "quantity is not a whole number of at least 1" });
      rec.quantity = String(Math.round(qty));
      let price = rec.unit_price ? toCents(rec.unit_price) : null;
      // IIF unit prices sit in PRICE; AMOUNT is the signed line total. A bill line has no
      // quantity, so its amount is already the unit price.
      if (table.source === "quickbooks-desktop") {
        const unit = toCents(rec.price ?? "");
        const total = toCents(rec.amount ?? "");
        if (unit != null && unit !== 0) price = Math.abs(unit);
        else if (total != null && total !== 0) price = Math.abs(total);
      }
      if (price == null || price <= 0) return rejected.push({ row: n, reason: "amount is not a positive number" });
      rec.unit_price = (price / 100).toFixed(2);
    }
    out.push(rec);
  });

  const unmapped = table.headers.filter((h) => !used.has(h));
  return { rows: out, rejected, unmapped };
}

/** Whole pipeline for one uploaded file: parse, identify, map. `as` overrides the guess for
 *  files that are identical whichever list they came from (a MYOB card export is both). */
export function convert(text: string, as?: Entity): { table: Table; mapped: Mapped } {
  const parsed = text.split("\n")[0].trimStart().startsWith("!") ? parseIIF(text) : parseTable(text);
  const source = detectSource(parsed.headers, text);
  if (!source) throw new Error("This file does not look like an export from QuickBooks, MYOB, Sage, Peachtree or standard CSV.");
  if (source === "quickbooks-desktop" && !as) return convertIIF(source, parsed);
  const entity = as ?? detectEntity(parsed.headers);
  if (!entity) throw new Error(`This looks like a ${label(source)} file, but the records in it are not accounts, contacts, items, invoices or bills.`);
  const table: Table = { source, entity, headers: parsed.headers, rows: parsed.rows };
  return { table, mapped: mapRows(table) };
}

/**
 * One IIF file can hold several record types plus line-breakdown (SPL) rows. Convert only
 * the rows for the detected entity: SPL rows are the line detail of their TRNS, not extra docs.
 */
function convertIIF(source: Source, parsed: { headers: string[]; rows: Record<string, string>[] }): { table: Table; mapped: Mapped } {
  const tags = new Set(parsed.rows.map((r) => r._tag));
  let entity: Entity | null = null;
  let keep = (_r: Record<string, string>) => true;
  if (tags.has("TRNS")) {
    const first = parsed.rows.find((r) => r._tag === "TRNS");
    const kind = norm(first?.TRNSTYPE ?? "");
    if (kind.includes("bill")) entity = "bills";
    else if (kind.includes("invoice")) entity = "invoices";
    else throw new Error(`This QuickBooks file holds ${first?.TRNSTYPE ?? "other"} records; only bills and invoices convert for now.`);
    // IIF splits a document in two: TRNS carries the party, date and control account, each
    // SPL carries one line's account, quantity and amount. Importing TRNS alone would bill
    // every line to Accounts Payable, so pair each SPL with its TRNS and import per line.
    const table: Table = { source, entity, headers: [], rows: pairIIFLines(parsed.rows.filter((r) => norm(r.TRNSTYPE ?? "") === kind)) };
    return { table, mapped: mapRows(table) };
  } else if (tags.has("ACCNT")) { entity = "accounts"; keep = (r) => r._tag === "ACCNT"; }
  else if (tags.has("CUST")) { entity = "customers"; keep = (r) => r._tag === "CUST"; }
  else if (tags.has("VEND")) { entity = "vendors"; keep = (r) => r._tag === "VEND"; }
  else if (tags.has("INVITEM")) { entity = "items"; keep = (r) => r._tag === "INVITEM"; }
  else throw new Error("This QuickBooks file holds none of accounts, contacts, items, invoices or bills.");
  const table: Table = { source, entity, headers: parsed.headers, rows: parsed.rows.filter(keep) };
  return { table, mapped: mapRows(table) };
}

/**
 * Turn QuickBooks IIF transaction rows into one flat row per line. TRNS supplies the document
 * (party, date, due date, reference); SPL supplies the line (account, quantity, amount, item).
 * A document with no SPL rows keeps its TRNS values so it is not silently dropped.
 */
function pairIIFLines(rows: Record<string, string>[]): Record<string, string>[] {
  const out: Record<string, string>[] = [];
  let head: Record<string, string> | null = null;
  for (const r of rows) {
    if (r._tag === "TRNS") { head = r; continue; }
    if (r._tag !== "SPL" || !head) continue;
    // Quantities are signed by accounting direction too, so a -2 on an invoice is 2 units sold.
    const qty = r.QNTY ? String(r.QNTY).replace("-", "") : "";
    // A sales-tax line carries a rate ("5.00%") and the AUTOSTAX marker. It is not income and
    // Tidal recalculates GST itself, so it is refused with a reason rather than mis-posted.
    const taxLine = /%/.test(String(r.PRICE ?? "")) || /AUTOSTAX/i.test(String(r.EXTRA ?? "")) ? "1" : "";
    out.push({
      ...head,
      DATE: head.DATE ?? "",
      DUEDATE: head.DUEDATE ?? "",
      DOCNUM: head.DOCNUM ?? "",
      MEMO: r.MEMO || head.MEMO || "",
      // Line values win over the control account: this is the expense or income side.
      ACCNT: r.ACCNT || head.ACCNT || "",
      QNTY: qty,
      PRICE: r.PRICE ?? "",
      AMOUNT: r.AMOUNT ?? "",
      INVITEM: r.INVITEM ?? "",
      TAXLINE: taxLine,
    });
  }
  if (!out.length && head) out.push(head);
  return out;
}

export const label = (s: Source) => ({
  xero: "Standard accounting CSV",
  quickbooks: "QuickBooks Online",
  "quickbooks-desktop": "QuickBooks Desktop",
  myob: "MYOB",
  sage: "Sage",
  peachtree: "Sage 50 US (Peachtree)",
}[s]);

/** Tidal import kind that receives this entity, so the caller reuses the existing importer. */
export const importKind = (e: Entity): string => ({
  accounts: "accounts", customers: "customers", vendors: "vendors",
  items: "items", invoices: "invoices", bills: "bills",
}[e]);

