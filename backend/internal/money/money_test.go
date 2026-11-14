package money

import "testing"

func TestParse(t *testing.T) {
	cases := map[string]Cents{
		"1,234.50":     123450,
		"(1,234.50)":   -123450,
		"-1234":        -123400,
		"S$ 1,234":     123400,
		"1,234.50 CR":  -123450,
		"1,234.50 Dr":  123450,
		"$(12.30)":     -1230,
		"—":            0,
		" 98,765.43 ":  9876543,
		"SGD 1,000.00": 100000,
	}
	for in, want := range cases {
		got, ok := Parse(in)
		if !ok || got != want {
			t.Errorf("Parse(%q) = %v,%v want %v", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "Revenue", "12a", "N/A"} {
		if _, ok := Parse(bad); ok {
			t.Errorf("Parse(%q) should fail", bad)
		}
	}
}

func TestRounding(t *testing.T) {
	if got := Cents(123450).RoundDollar(); got != 123500 {
		t.Errorf("round half up: %v", got)
	}
	if got := Cents(-123450).RoundDollar(); got != -123500 {
		t.Errorf("round negative: %v", got)
	}
	if got := Cents(1000000).MulRate(1700); got != 170000 {
		t.Errorf("17%%: %v", got)
	}
	if got := Cents(-123456789).String(); got != "-1,234,567.89" {
		t.Errorf("String: %s", got)
	}
}

// A separator in the wrong place or a wild magnitude is refused, not read as a different amount.
func TestParseRefusesMalformedAmounts(t *testing.T) {
	for _, s := range []string{"1,23.45", "12,34", "1234,567", ",123", "1,2345", "1e30", "999999999999999999", "1.2.3", "abc"} {
		if c, ok := Parse(s); ok {
			t.Fatalf("%q was accepted as %d cents", s, c)
		}
	}
	for _, s := range []string{"1,234.50", "1234.5", "S$ 1,234", "(1,234.50)", "1,234.50 CR", "999,999,999.99"} {
		if _, ok := Parse(s); !ok {
			t.Fatalf("%q was refused", s)
		}
	}
}
