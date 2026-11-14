import { Inter } from "next/font/google";

// Inter everywhere inside the app, headings included. Tabular figures are set in CSS so money lines
// up in tables and KPI tiles. The public landing page keeps its own display styling.
export const body = Inter({ subsets: ["latin"], display: "swap", variable: "--ff-body" });
