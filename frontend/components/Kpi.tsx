// A single dashboard figure: label, value, and one quiet secondary line (the period it covers).
export default function Kpi({ label, value, note }: { label: string; value: string; note?: string }) {
  return (
    <div className="kpi">
      <p className="kpi-label">{label}</p>
      <p className="kpi-value">{value}</p>
      {note && <p className="kpi-note">{note}</p>}
    </div>
  );
}
