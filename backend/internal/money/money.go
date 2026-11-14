// Package money handles SGD amounts as integer cents.
package money

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Cents is an SGD amount in cents.
type Cents int64

// FromDollars converts a float dollar amount, rounding half away from zero.
func FromDollars(d float64) Cents { return Cents(math.Round(d * 100)) }

// Dollars returns the amount as float dollars (display only).
func (c Cents) Dollars() float64 { return float64(c) / 100 }

// RoundDollar rounds to the nearest whole dollar (half away from zero), as IRAS forms take whole dollars.
func (c Cents) RoundDollar() Cents {
	if c >= 0 {
		return (c + 50) / 100 * 100
	}
	return -((-c + 50) / 100 * 100)
}

// WholeDollars returns the amount as an integer number of dollars after rounding.
func (c Cents) WholeDollars() int64 { return int64(c.RoundDollar()) / 100 }

// MulRate multiplies by a rate given in basis points (1% = 100 bp), rounding to the cent.
func (c Cents) MulRate(bp int64) Cents {
	v := int64(c) * bp
	if v >= 0 {
		return Cents((v + 5000) / 10000)
	}
	return Cents(-((-v + 5000) / 10000))
}

func Max(a, b Cents) Cents {
	if a > b {
		return a
	}
	return b
}

func Min(a, b Cents) Cents {
	if a < b {
		return a
	}
	return b
}

func Abs(a Cents) Cents {
	if a < 0 {
		return -a
	}
	return a
}

// String formats like 1,234.56 with a leading minus for negatives.
func (c Cents) String() string {
	neg := c < 0
	v := int64(Abs(c))
	whole, frac := v/100, v%100
	s := strconv.FormatInt(whole, 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := fmt.Sprintf("%s.%02d", b.String(), frac)
	if neg {
		return "-" + out
	}
	return out
}

// Parse reads amounts as they appear in accounting exports: "1,234.50", "(1,234.50)", "-1234",
// "S$ 1,234", "1,234.50 CR", "1,234.50 Dr", "—" (zero). It returns ok=false for non-numeric text.
func Parse(s string) (Cents, bool) {
	t := strings.TrimSpace(s)
	if t == "" || t == "-" || t == "—" || t == "–" {
		return 0, t != ""
	}
	neg := false
	upper := strings.ToUpper(t)
	switch {
	case strings.HasSuffix(upper, "CR"):
		neg = true
		t = strings.TrimSpace(t[:len(t)-2])
	case strings.HasSuffix(upper, "DR"):
		t = strings.TrimSpace(t[:len(t)-2])
	}
	t = stripCurrency(t)
	if strings.HasPrefix(t, "(") && strings.HasSuffix(t, ")") {
		neg = !neg
		t = stripCurrency(t[1 : len(t)-1])
	}
	if strings.HasPrefix(t, "-") {
		neg = !neg
		t = strings.TrimSpace(t[1:])
	}
	t = strings.ReplaceAll(t, " ", "")
	// A separator in the wrong place ("1,23.45") is a reading mistake in the source, not a
	// smaller number: refusing it keeps the figure out of the books instead of inventing one.
	if !validAmountFormat(t) {
		return 0, false
	}
	t = strings.ReplaceAll(t, ",", "")
	if t == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) || math.Abs(f) > maxDollars {
		return 0, false
	}
	c := FromDollars(f)
	if neg {
		c = -c
	}
	return c, true
}

// maxDollars bounds a single amount well inside int64 cents, so a wild figure ("1e30") is
// refused instead of silently wrapping to a negative balance.
const maxDollars = 1e14

// validAmountFormat accepts digits with correctly grouped thousands separators and an optional
// decimal part.
func validAmountFormat(t string) bool {
	whole, fraction, hasFraction := strings.Cut(t, ".")
	if hasFraction && (fraction == "" || !allDigits(fraction)) {
		return false
	}
	if !strings.Contains(whole, ",") {
		return allDigits(whole)
	}
	groups := strings.Split(whole, ",")
	if len(groups[0]) < 1 || len(groups[0]) > 3 || !allDigits(groups[0]) {
		return false
	}
	for _, g := range groups[1:] {
		if len(g) != 3 || !allDigits(g) {
			return false
		}
	}
	return true
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func stripCurrency(t string) string {
	for _, p := range []string{"SGD", "S$", "$"} {
		t = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(t, p), strings.ToLower(p)))
	}
	return t
}
