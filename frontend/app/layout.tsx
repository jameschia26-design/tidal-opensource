import type { Metadata } from "next";
import "./globals.css";
import "./globals-app.css";
import { body } from "./fonts";

export const metadata: Metadata = {
  title: { default: "Tidal", template: "%s · Tidal" },
  description: "Accounting, invoicing and Singapore tax worksheets for small and medium businesses.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en-SG" className={body.variable}>
      <body>{children}</body>
    </html>
  );
}
