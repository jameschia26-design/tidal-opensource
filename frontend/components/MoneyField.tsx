"use client";
import { useId, useRef } from "react";
import { formatMoneyInput } from "@/lib/money";

// One labelled SGD amount. Auto-formats with commas; page converts to integer cents on submit.
export default function MoneyField({
  label,
  value,
  onChange,
  help,
  placeholder,
  required,
  disabled,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  help?: string;
  placeholder?: string;
  required?: boolean;
  disabled?: boolean;
}) {
  const id = useId();
  const inputRef = useRef<HTMLInputElement>(null);

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const el = e.target;
    const raw = el.value;
    const cursor = el.selectionEnd ?? raw.length;
    const nonCommasBefore = raw.slice(0, cursor).replace(/,/g, "").length;
    const formatted = formatMoneyInput(raw);
    onChange(formatted);

    requestAnimationFrame(() => {
      if (inputRef.current) {
        let count = 0;
        let newPos = formatted.length;
        for (let i = 0; i < formatted.length; i++) {
          if (formatted[i] !== ",") count++;
          if (count === nonCommasBefore) {
            newPos = i + 1;
            break;
          }
        }
        if (nonCommasBefore === 0) newPos = 0;
        inputRef.current.setSelectionRange(newPos, newPos);
      }
    });
  };

  return (
    <div>
      <label htmlFor={id}>{label}</label>
      <input
        ref={inputRef}
        id={id}
        inputMode="decimal"
        placeholder={placeholder}
        required={required}
        disabled={disabled}
        value={value}
        onChange={handleChange}
        aria-describedby={help ? `${id}-h` : undefined}
      />
      {help && <p id={`${id}-h`} className="muted small">{help}</p>}
    </div>
  );
}
