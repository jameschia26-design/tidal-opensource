package taxrules

import (
	"testing"
	"tidal/backend/internal/money"
)

func TestPITBalancingChargeAndAllowanceAreSeparate(t *testing.T) {
	for _, tc := range []struct {
		name                                                 string
		profit, charge, allowance, adjusted, statutory, loss int64
	}{
		{"charge only", 0, 20000, 0, 20000, 20000, 0},
		{"charge and allowances", 100000, 20000, 50000, 120000, 70000, 0},
		{"charge reduces trade loss", -30000, 20000, 0, -10000, 0, 10000},
		{"ordinary allowance", 100000, 0, 50000, 100000, 50000, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := PITInput{YA: 2026, Age: 40, Resident: true, GrossProfit: money.Cents(tc.profit)}
			in.AddCapitalAllowanceSchedule(CASchedule{TotalAllowance: money.Cents(tc.allowance), BalancingCharge: money.Cents(tc.charge)})
			result, err := ComputePIT(in)
			if err != nil {
				t.Fatal(err)
			}
			if int64(result.AdjustedProfit) != tc.adjusted || int64(result.StatutoryBusiness) != tc.statutory || int64(result.CurrentYearLossCF) != tc.loss {
				t.Fatalf("unexpected tax result: %+v", result)
			}
		})
	}
	if _, err := ComputePIT(PITInput{YA: 2026, Age: 40, BalancingCharge: -1}); err == nil {
		t.Fatal("negative balancing charge accepted")
	}
}
