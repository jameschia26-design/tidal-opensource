import type { ReactNode } from "react";

export type Column<T> = { key: string; header: string; num?: boolean; render: (row: T) => ReactNode };

// Shared list: caption, optional empty state with a create action, right-aligned numeric columns.
export default function ListTable<T>({ caption, columns, rows, rowKey, empty }: {
  caption: string; columns: Column<T>[]; rows: T[]; rowKey: (row: T) => string; empty?: ReactNode;
}) {
  if (rows.length === 0) return <div className="notice" role="status">{empty ?? "Nothing here yet."}</div>;
  return (
    <div className="table-scroll">
      <table className="doc-table">
        <caption className="visually-hidden">{caption}</caption>
        <thead><tr>{columns.map((c) => <th key={c.key} scope="col" className={c.num ? "num" : undefined}>{c.header}</th>)}</tr></thead>
        <tbody>{rows.map((r) => <tr key={rowKey(r)}>{columns.map((c) => <td key={c.key} className={c.num ? "num" : undefined}>{c.render(r)}</td>)}</tr>)}</tbody>
      </table>
    </div>
  );
}
