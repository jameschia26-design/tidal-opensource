import { formatCents } from "@/lib/money";
import { previousAmount, type ReportBlock } from "@/lib/report-shape";

// Renders the grouped statement blocks: one table per section with a real heading and a total row,
// then the computed lines (gross profit, operating profit, net profit, net assets) as their own
// rows. The report toolbar exports these rendered rows, so the CSV carries the same sections.
export default function ReportBlocks({ blocks, showPrevious }: { blocks: ReportBlock[]; showPrevious: boolean }) {
  const columns = showPrevious ? 4 : 3;
  return (
    <>
      {blocks.map((block) => block.kind === "section" ? (
        <section key={block.key} aria-labelledby={`section-${block.key}`}>
          <h3 id={`section-${block.key}`}>{block.label}</h3>
          <table className="doc-table">
            <caption className="sr-only">{block.label}</caption>
            <thead>
              <tr>
                <th scope="col">Code</th>
                <th scope="col">Account</th>
                <th scope="col" className="num">Amount</th>
                {showPrevious && <th scope="col" className="num">Previous year</th>}
              </tr>
            </thead>
            <tbody>
              {block.lines.length === 0 && <tr><td colSpan={columns} className="muted">No accounts in this section.</td></tr>}
              {block.lines.map((line) => (
                <tr key={line.code + line.name}>
                  <td>{line.code}</td>
                  <td>{line.name}</td>
                  <td className="num">{formatCents(line.amount_cents)}</td>
                  {showPrevious && <td className="num">{formatCents(previousAmount(block.previousLines, line))}</td>}
                </tr>
              ))}
            </tbody>
            <tfoot>
              <tr>
                <th scope="row" colSpan={2}>{block.totalLabel}</th>
                <td className="num">{formatCents(block.total_cents)}</td>
                {showPrevious && <td className="num">{formatCents(block.previous_cents)}</td>}
              </tr>
            </tfoot>
          </table>
        </section>
      ) : (
        <table className="doc-table" key={block.key}>
          <caption className="sr-only">{block.label}</caption>
          <tbody>
            <tr>
              <th scope="row" colSpan={2}>{block.label}</th>
              <td className="num"><strong>{formatCents(block.amount_cents)}</strong></td>
              {showPrevious && <td className="num">{formatCents(block.previous_cents)}</td>}
            </tr>
          </tbody>
        </table>
      ))}
    </>
  );
}
