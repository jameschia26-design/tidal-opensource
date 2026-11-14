import type { ReactNode } from "react";

// Grouped fieldset used by every form: legend, optional help text, two-column grid.
export default function FormSection({ legend, help, children }: { legend: string; help?: string; children: ReactNode }) {
  return (
    <fieldset>
      <legend>{legend}</legend>
      {help && <p className="section-help">{help}</p>}
      <div className="form-grid">{children}</div>
    </fieldset>
  );
}
