// Rates and per-line rounding match the credit-note server calculation.
export function creditGstRate(date: string): number {
  return date >= "2024-01-01" ? 9 : date >= "2023-01-01" ? 8 : 7;
}

export function creditGstCents(amount: number, code: string, rate: number): number {
  return code === "SR" ? Math.round(amount * rate / 100) : 0;
}
