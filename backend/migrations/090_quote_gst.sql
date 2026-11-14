-- Quotes can carry GST per line, like invoices. Stored total = net after commercial adjustments + line GST.
ALTER TABLE quotes DROP CONSTRAINT quote_lines_commercial_total;
ALTER TABLE quotes ADD CONSTRAINT quote_lines_commercial_total CHECK (
 valid_quote_lines(lines, quote_line_subtotal(lines)) AND commercial_total(quote_line_subtotal(lines), commercial) + quote_line_gst(lines) = total_cents
);
