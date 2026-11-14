export type CompanyAddress = { attention: string; line1: string; line2: string; city: string; region: string; postal_code: string; country: string };

const fields: (keyof CompanyAddress)[] = ["attention", "line1", "line2", "city", "region", "postal_code", "country"];

// Compares the stored address fields, not object identity or key order.
export const sameCompanyAddress = (a: CompanyAddress, b: CompanyAddress) => fields.every(field => a[field] === b[field]);
