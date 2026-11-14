// Minimal CSV parser: quoted fields, doubled quotes, CRLF or LF. Returns rows of trimmed cells; blank lines are skipped.
// A quote left open at end of input is rejected with its row number, matching how the Go importer errors.
export function parseCsv(text: string): string[][] {
  const rows: string[][] = [];
  let row: string[] = [];
  let cell = "";
  let quoted = false;
  let quotedRow = 0;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (quoted) {
      if (c === '"' && text[i + 1] === '"') { cell += '"'; i++; }
      else if (c === '"') quoted = false;
      else cell += c;
    } else if (c === '"') { if (cell !== "") throw new Error(`Bad quote on row ${rows.length + 1}`); quoted = true; quotedRow = rows.length + 1; }
    else if (c === ",") { row.push(cell.trim()); cell = ""; }
    else if (c === "\n" || c === "\r") {
      if (c === "\r" && text[i + 1] === "\n") i++;
      row.push(cell.trim()); cell = "";
      if (row.some(x => x !== "")) rows.push(row);
      row = [];
    } else cell += c;
  }
  if (quoted) throw new Error(`Unterminated quote starting on row ${quotedRow || rows.length + 1}`);
  if (cell.includes('"')) throw new Error(`Bad quote on row ${rows.length + 1}`);
  row.push(cell.trim());
  if (row.some(x => x !== "")) rows.push(row);
  return rows;
}

// Column order the bank statement importer expects. The server treats the first "date" row as a header.
export const BANK_STATEMENT_HEADERS = ["date", "description", "reference", "amount"];

// Example cell per column, keyed by column name so a template cannot drift from its spec headers.
// Dates stay ISO YYYY-MM-DD because every importer parses that format.
const CSV_EXAMPLES: Record<string, string> = {
  name: "Acme Pte Ltd", customer: "Acme Pte Ltd", company_name: "Acme Pte Ltd", vendor: "Office Supplies Pte Ltd",
  code: "6100", type: "expense", item_type: "service", sku: "CONSULT-1", selling_price: "500.00",
  bill_date: "2026-01-31", account_code: "6000", bill_number: "BILL-1001",
  email: "billing@acme.sg", phone: "+65 6123 4567", contact_type: "business", payment_terms_days: "30",
  date: "2026-01-31", invoice_date: "2026-01-31", due_date: "2026-02-28",
  quote_date: "2026-01-31", expiry_date: "2026-02-28", credit_date: "2026-01-31",
  receipt_date: "2026-01-31", payment_date: "2026-01-31", expense_date: "2026-01-31", start_date: "2026-01-31",
  description: "Consulting in January", quantity: "1", unit_price: "500.00", amount: "500.00",
  payee: "Office Supplies Pte Ltd", reference: "INV-1001", invoice_number: "INV-1001",
  deposit_account_code: "1010", income_account_code: "4000", expense_account_code: "6000",
  paid_through_account_code: "1010", frequency: "monthly",
};

// Text cells that a spreadsheet would treat as a formula are prefixed so they stay text (R20).
// Numbers such as -12.50 keep their sign, so amount columns stay numeric.
export function csvCell(v: string): string {
  const formula = /^[=+@\t\r]/.test(v) || /^-[^\d.]/.test(v);
  const safe = formula ? `'${v}` : v;
  return /[",\n\r]/.test(safe) ? `"${safe.replace(/"/g, '""')}"` : safe;
}

// Header row plus one example row, built from the same header list the importer checks.
export function csvTemplate(headers: string[]): string {
  const example = headers.map(h => CSV_EXAMPLES[h] ?? "example");
  return [headers.map(csvCell).join(","), example.map(csvCell).join(",")].join("\n") + "\n";
}

// Client-side download of a CSV string; the file is made in the browser, nothing is uploaded.
export function downloadCsv(filename: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: "text/csv;charset=utf-8" }));
  const link = document.createElement("a");
  link.href = url; link.download = filename; link.click();
  URL.revokeObjectURL(url);
}

// ponytail: no header auto-mapping beyond exact names; a quoted newline inside a cell is kept as-is.
// parseCsv throws on unterminated or misplaced quotes, mirroring the Go importer's strictness.
export function selfCheckCsv(): boolean {
  const good = parseCsv('name,email\r\n"Acme, Pte",a@x.sg\n"say ""hi""",\n\n');
  const ok = JSON.stringify(good) === JSON.stringify([["name", "email"], ["Acme, Pte", "a@x.sg"], ['say "hi"', ""]]);
  let unterminated = 0;
  try { parseCsv('name\n"open,x\n'); } catch { unterminated++; }
  try { parseCsv('name\nopen,"still open\n'); } catch { unterminated++; }
  return ok && unterminated === 2;
}
