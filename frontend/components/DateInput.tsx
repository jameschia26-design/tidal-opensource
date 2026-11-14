"use client";
import { useEffect, useId, useRef, useState, type CSSProperties } from "react";
import { autoSlashDate, dateOnlyBound, formatDate, isDateOnly, parseDMY } from "@/lib/dates";

// Text box that shows dd/mm/yyyy with auto slash and native popup calendar,
// but speaks ISO to the rest of the app: value in, value out.
type Props = {
  value: string;
  onChange: (e: { target: { value: string } }) => void;
  required?: boolean;
  disabled?: boolean;
  readOnly?: boolean;
  min?: string;
  max?: string;
  className?: string;
  id?: string;
  name?: string;
  title?: string;
  style?: CSSProperties;
  "aria-label"?: string;
};

export default function DateInput({ value, onChange, min, max, required, disabled, readOnly, className, id, name, title, style, "aria-label": ariaLabel }: Props) {
  const autoId = useId();
  const inputId = id ?? autoId;
  const hintId = `${inputId}-hint`;
  const ref = useRef<HTMLInputElement>(null);
  const pickerRef = useRef<HTMLInputElement>(null);
  const [text, setText] = useState(() => formatDate(value));
  const [seen, setSeen] = useState(value);

  // Follow outside changes (defaults, resets, "use today") without clobbering half-typed text.
  /* eslint-disable react-hooks/set-state-in-effect -- mirroring a prop into editable text is the effect's job */
  useEffect(() => {
    if (value === seen) return;
    setSeen(value);
    setText((t) => (parseDMY(t) !== value ? formatDate(value) : t));
  }, [value, seen]);
  /* eslint-enable react-hooks/set-state-in-effect */

  const minDate = dateOnlyBound(min);
  const maxDate = dateOnlyBound(max);
  const typed = text.trim();
  const parsed = typed ? parseDMY(typed) : "";
  const storedNotADate = Boolean(value) && !isDateOnly(value);
  const message = typed && !parsed
    ? storedNotADate && typed === value.trim()
      ? "The saved value is not a date. Type it as dd/mm/yyyy."
      : "Enter the date as dd/mm/yyyy."
    : parsed && minDate && parsed < minDate ? `Enter ${formatDate(minDate)} or later.`
      : parsed && maxDate && parsed > maxDate ? `Enter ${formatDate(maxDate)} or earlier.`
        : "";

  useEffect(() => { ref.current?.setCustomValidity(message); }, [message]);

  const openPicker = () => {
    if (disabled || readOnly) return;
    try {
      pickerRef.current?.showPicker();
    } catch {
      pickerRef.current?.focus();
    }
  };

  return (
    <div className="date-input-wrap" style={{ display: "inline-flex", alignItems: "center", position: "relative" }}>
      <input
        ref={ref}
        id={inputId}
        name={name}
        type="text"
        inputMode="numeric"
        placeholder="dd/mm/yyyy"
        autoComplete="off"
        title={title}
        style={style}
        className={className}
        value={text}
        required={required}
        disabled={disabled}
        readOnly={readOnly}
        aria-label={ariaLabel}
        aria-invalid={message ? true : undefined}
        aria-describedby={hintId}
        onChange={(e) => {
          const raw = e.target.value;
          const next = autoSlashDate(raw, text);
          setText(next);
          if (!next.trim()) { onChange({ target: { value: "" } }); return; }
          const iso = parseDMY(next);
          if (iso) onChange({ target: { value: iso } });
        }}
      />
      {/* Hidden native date input for pop up calendar */}
      <input
        ref={pickerRef}
        type="date"
        tabIndex={-1}
        aria-hidden="true"
        min={minDate}
        max={maxDate}
        value={parsed || ""}
        disabled={disabled || readOnly}
        style={{ position: "absolute", opacity: 0, width: 0, height: 0, pointerEvents: "none" }}
        onChange={(e) => {
          const picked = e.target.value;
          if (picked) {
            setText(formatDate(picked));
            onChange({ target: { value: picked } });
          }
        }}
      />
      <button
        type="button"
        className="date-picker-btn"
        aria-label="Open calendar"
        title="Open calendar"
        disabled={disabled || readOnly}
        onClick={openPicker}
        style={{
          marginLeft: "-2rem",
          background: "none",
          border: "none",
          cursor: disabled || readOnly ? "default" : "pointer",
          padding: "0.25rem",
          display: "inline-flex",
          alignItems: "center",
          color: "var(--color-ink-muted, #4a5c56)",
          zIndex: 1,
        }}
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" aria-hidden="true">
          <rect x="3" y="4" width="18" height="18" rx="2" ry="2" />
          <line x1="16" y1="2" x2="16" y2="6" />
          <line x1="8" y1="2" x2="8" y2="6" />
          <line x1="3" y1="10" x2="21" y2="10" />
        </svg>
      </button>
      <span id={hintId} className="visually-hidden">Date as dd/mm/yyyy</span>
    </div>
  );
}
