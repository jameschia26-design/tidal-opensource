import Link from "next/link";
import ContactsPage from "@/components/ContactsPage";

export default function SuppliersPage() {
  return <><nav className="form-actions" aria-label="Purchases"><Link href="/purchases">Bills</Link><Link href="/purchases/suppliers" aria-current="page">Suppliers</Link></nav><ContactsPage role="supplier" /></>;
}
