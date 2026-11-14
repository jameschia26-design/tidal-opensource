package main

import (
	"math"
	"testing"
	"time"

	"tidal/backend/internal/money"
	"tidal/backend/internal/taxrules"
)

// Unit checks for core rules that need no database.
func TestBuildInvoiceLinesComputesServerTotals(t *testing.T) {
	lines, total, code := buildInvoiceLines([]invoiceLineIn{
		{Description: "Consulting", Quantity: 3, UnitPriceCents: 12050},
		{Description: "Hosting", Quantity: 1, UnitPriceCents: 999, GSTCode: "none"},
	})
	if code != "" {
		t.Fatalf("unexpected code %q", code)
	}
	if total != 3*12050+999 {
		t.Fatalf("total = %d", total)
	}
	if lines[0].AmountCents != 36150 || lines[1].LineNo != 2 {
		t.Fatalf("lines = %+v", lines)
	}
}

func TestBuildInvoiceLinesRejectsGSTAndBadAmounts(t *testing.T) {
	cases := []struct {
		name  string
		lines []invoiceLineIn
		want  string
	}{
		{"gst code refused by plain builder", []invoiceLineIn{{Description: "x", Quantity: 1, UnitPriceCents: 100, GSTCode: "SR"}}, "unsupported_gst"},
		{"zero qty", []invoiceLineIn{{Description: "x", Quantity: 0, UnitPriceCents: 100}}, "invalid_input"},
		{"negative price", []invoiceLineIn{{Description: "x", Quantity: 1, UnitPriceCents: -1}}, "invalid_input"},
		{"blank desc", []invoiceLineIn{{Description: "  ", Quantity: 1, UnitPriceCents: 100}}, "invalid_input"},
		{"NUL description", []invoiceLineIn{{Description: "bad\x00text", Quantity: 1, UnitPriceCents: 100}}, "invalid_input"},
		{"invalid UTF8 description", []invoiceLineIn{{Description: string([]byte{255}), Quantity: 1, UnitPriceCents: 100}}, "invalid_input"},
		{"zero total", []invoiceLineIn{{Description: "x", Quantity: 1, UnitPriceCents: 0}}, "invalid_input"},
		{"overflow", []invoiceLineIn{{Description: "x", Quantity: 1000000, UnitPriceCents: maxMoneyCents}}, "invalid_input"},
		{"no lines", nil, "invalid_input"},
	}
	for _, c := range cases {
		if _, _, code := buildInvoiceLines(c.lines); code != c.want {
			t.Errorf("%s: code = %q, want %q", c.name, code, c.want)
		}
	}
}

func TestStageTransitionsAreTerminalAtLost(t *testing.T) {
	if leadNext["lost"]["new"] || leadNext["lost"]["qualified"] {
		t.Fatal("lost lead must not move")
	}
	if !leadNext["new"]["qualified"] || leadNext["new"]["won"] {
		t.Fatal("lead transitions wrong")
	}
	if dealNext["won"]["open"] || dealNext["lost"]["open"] {
		t.Fatal("won/lost deal must be terminal")
	}
	if !dealNext["open"]["won"] {
		t.Fatal("open deal must be able to win")
	}
}

func TestNetSidesReducesToOneSide(t *testing.T) {
	if d, c := netSides(balRow{Debit: 10000, Credit: 4000}); d != 6000 || c != 0 {
		t.Fatalf("debit-heavy = %d/%d", d, c)
	}
	if d, c := netSides(balRow{Debit: 4000, Credit: 10000}); d != 0 || c != 6000 {
		t.Fatalf("credit-heavy = %d/%d", d, c)
	}
}

func TestTaxBoundsRejectExtremesAndOverflow(t *testing.T) {
	if citInputBounded(taxrules.CITInput{Revenue: money.Cents(math.MinInt64)}) {
		t.Fatal("MinInt64 accepted")
	}
	if citInputBounded(taxrules.CITInput{Revenue: money.Cents(math.MaxInt64)}) {
		t.Fatal("MaxInt64 accepted")
	}
	if !citInputBounded(taxrules.CITInput{Revenue: maxTaxAbsTotal}) {
		t.Fatal("ceiling itself rejected")
	}
	if citInputBounded(taxrules.CITInput{Revenue: maxTaxAbsTotal + 1}) {
		t.Fatal("over ceiling accepted")
	}
	// Three items of 4e11 each: every one is in range, but the aggregate exceeds the ceiling.
	big := []taxrules.Item{{Label: "a", Amount: 400000000000}, {Label: "b", Amount: 400000000000}, {Label: "c", Amount: 400000000000}}
	if citInputBounded(taxrules.CITInput{SeparateSourceIncome: big}) {
		t.Fatal("aggregate over ceiling accepted")
	}
	// Negative amounts count by absolute value, so offsetting values cannot hide a large sum.
	if citInputBounded(taxrules.CITInput{Revenue: 900000000000, NetProfitBeforeTax: -900000000000, NonDeductible: []taxrules.Item{{Amount: 200000000000}}}) {
		t.Fatal("offsetting aggregate accepted")
	}
	if pitInputBounded(taxrules.PITInput{Revenue: money.Cents(math.MinInt64)}) {
		t.Fatal("PIT MinInt64 accepted")
	}
	if pitInputBounded(taxrules.PITInput{Reliefs: big}) {
		t.Fatal("PIT relief aggregate accepted")
	}
	if !gstLinesBounded(0, []gstLineIn{{NetCents: 100, GSTCents: 9}}) {
		t.Fatal("normal GST line rejected")
	}
	if gstLinesBounded(0, []gstLineIn{{NetCents: money.Cents(math.MinInt64), GSTCents: 0}}) {
		t.Fatal("GST MinInt64 accepted")
	}
}

func TestItemCountCeiling(t *testing.T) {
	items := make([]taxrules.Item, maxTaxItems)
	for i := range items {
		items[i] = taxrules.Item{Amount: 1}
	}
	if !citInputBounded(taxrules.CITInput{NonDeductible: items}) {
		t.Fatal("200 items rejected")
	}
	items = append(items, taxrules.Item{Amount: 1})
	if citInputBounded(taxrules.CITInput{NonDeductible: items}) {
		t.Fatal("201 items accepted")
	}
}

func TestGSTPeriodRejectsRateChangeAndLongPeriods(t *testing.T) {
	d := func(s string) time.Time { v, _ := time.Parse("2006-01-02", s); return v }
	if gstPeriodSupported(d("2022-12-01"), d("2023-01-31")) {
		t.Fatal("period spanning 2023 rate change accepted")
	}
	if gstPeriodSupported(d("2024-01-01"), d("2026-01-01")) {
		t.Fatal("two-year period accepted")
	}
	if !gstPeriodSupported(d("2026-01-01"), d("2026-03-31")) {
		t.Fatal("normal quarter rejected")
	}
}

func TestBuildSalesLinesGST(t *testing.T) {
	lines, net, gst, code := buildSalesLines([]invoiceLineIn{{Description: "x", Quantity: 1, UnitPriceCents: 100, GSTCode: "SR"}}, true, 900, false)
	if code != "" || net != 100 || gst != 9 || len(lines) != 1 || lines[0].GSTRateBP != 900 {
		t.Fatalf("registered SR result = %#v net=%d gst=%d code=%q", lines, net, gst, code)
	}
	if _, _, _, code := buildSalesLines([]invoiceLineIn{{Description: "x", Quantity: 1, UnitPriceCents: 100, GSTCode: "SR"}}, false, 900, false); code != "unsupported_gst" {
		t.Fatalf("unregistered SR code = %q, want unsupported_gst", code)
	}
	lines, _, gst, code = buildSalesLines([]invoiceLineIn{{Description: "x", Quantity: 1, UnitPriceCents: 100, GSTCode: "NONE"}}, true, 900, false)
	if code != "" || gst != 0 || lines[0].GSTCode != "NONE" {
		t.Fatalf("NONE result = %#v gst=%d code=%q", lines, gst, code)
	}
	// Tax-inclusive: a 109.00 line at 9% holds 100.00 net and 9.00 GST, and the total is unchanged.
	lines, net, gst, code = buildSalesLines([]invoiceLineIn{{Description: "x", Quantity: 1, UnitPriceCents: 10900, GSTCode: "SR"}}, true, 900, true)
	if code != "" || net != 10000 || gst != 900 || lines[0].AmountCents != 10000 || lines[0].GSTCents != 900 || net+gst != 10900 {
		t.Fatalf("inclusive result = %#v net=%d gst=%d code=%q", lines, net, gst, code)
	}
	// A 1.00 inclusive line at 9% splits 0.92 net + 0.08 GST with half-up rounding.
	lines, net, gst, _ = buildSalesLines([]invoiceLineIn{{Description: "x", Quantity: 1, UnitPriceCents: 100, GSTCode: "SR"}}, true, 900, true)
	if net != 92 || gst != 8 || lines[0].AmountCents+lines[0].GSTCents != 100 {
		t.Fatalf("inclusive rounding = net %d gst %d line %#v", net, gst, lines[0])
	}
}
