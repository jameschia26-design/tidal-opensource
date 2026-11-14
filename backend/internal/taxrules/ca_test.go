package taxrules

import "testing"

func TestCAThreeYearAndDisposal(t *testing.T) {
	van := Asset{Description: "Delivery van", Cost: d(30000), AcquiredYA: 2024, Method: CA3Year}
	laptop := Asset{Description: "Laptops", Cost: d(4000), AcquiredYA: 2026, Method: CA1Year}
	chair := Asset{Description: "Chairs", Cost: d(1000), AcquiredYA: 2026, Method: CALowVal}
	s, err := ComputeCA([]Asset{van, laptop, chair}, 2026)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "total", s.TotalAllowance, d(10000)+d(4000)+d(1000))
	check(t, "van twdv c/f", s.Lines[0].TWDVCarriedForward, 0)

	van.DisposedYA, van.DisposalProceeds = 2026, d(12000)
	s, _ = ComputeCA([]Asset{van}, 2026)
	// TWDV b/f 10,000, proceeds 12,000 -> balancing charge 2,000.
	check(t, "BC", s.BalancingCharge, d(2000))
	check(t, "no allowance in disposal year", s.TotalAllowance, 0)

	van.DisposalProceeds = d(4000)
	s, _ = ComputeCA([]Asset{van}, 2026)
	check(t, "BA", s.BalancingAllowance, d(6000))
}

func TestCAAllowanceDoesNotExceedRemainingCost(t *testing.T) {
	a := Asset{Description: "Machine", Cost: d(12000), AcquiredYA: 2025, Method: CA3Year, ClaimsConfirmedThroughYA: 2025, ClaimHistory: []CAClaim{{YA: 2025, Amount: d(9000)}}}
	s, err := ComputeCA([]Asset{a}, 2026)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "allowance", s.TotalAllowance, 0)
	check(t, "remaining value", s.Lines[0].TWDVCarriedForward, d(3000))
	if len(s.Warnings) == 0 {
		t.Fatal("inconsistent prior claims must require review")
	}
}

func TestCANoneRequiresConfirmedClaimHistory(t *testing.T) {
	a := Asset{Description: "Private car", Cost: d(12000), AcquiredYA: 2025, Method: CANone}
	s, err := ComputeCA([]Asset{a}, 2026)
	if err != nil {
		t.Fatal(err)
	}
	if s.Lines[0].ClaimHistoryComplete {
		t.Fatal("unconfirmed history must require review")
	}
	a.ClaimsConfirmedThroughYA = 2025
	s, err = ComputeCA([]Asset{a}, 2026)
	if err != nil || !s.Lines[0].ClaimHistoryComplete {
		t.Fatal("confirmed empty history should be complete", err)
	}
	check(t, "allowance", s.TotalAllowance, 0)
	a.ClaimHistory = []CAClaim{{YA: 2025, Amount: d(4000)}}
	a.DisposedYA, a.DisposalProceeds = 2026, d(12000)
	s, err = ComputeCA([]Asset{a}, 2026)
	if err != nil {
		t.Fatal(err)
	}
	if s.Lines[0].ClaimHistoryComplete || len(s.Warnings) == 0 {
		t.Fatal("prior claims on an ineligible asset must require review")
	}
}

func TestCADeferredThreeYearIRASExample(t *testing.T) {
	a := Asset{Description: "Machine", Cost: d(120000), AcquiredYA: 2017, Method: CA3Year, ClaimsConfirmedThroughYA: 2019, ClaimHistory: []CAClaim{{YA: 2018, Amount: d(40000)}}}
	s, err := ComputeCA([]Asset{a}, 2020)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "YA2020 allowance", s.TotalAllowance, d(40000))
	check(t, "YA2020 balance", s.Lines[0].TWDVCarriedForward, d(40000))
	a.ClaimsConfirmedThroughYA = 2020
	a.ClaimHistory = append(a.ClaimHistory, CAClaim{YA: 2020, Amount: d(40000)})
	s, err = ComputeCA([]Asset{a}, 2021)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "YA2021 allowance", s.TotalAllowance, d(40000))
	check(t, "YA2021 balance", s.Lines[0].TWDVCarriedForward, 0)
}

func TestCADeferredOneYearAndAmbiguousHistory(t *testing.T) {
	a := Asset{Description: "Computer", Cost: d(3000), AcquiredYA: 2024, Method: CA1Year, ClaimsConfirmedThroughYA: 2025}
	s, err := ComputeCA([]Asset{a}, 2026)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "deferred computer", s.TotalAllowance, d(3000))
	a.ClaimHistory = []CAClaim{{YA: 2025, Amount: d(1000)}}
	s, err = ComputeCA([]Asset{a}, 2026)
	if err != nil || len(s.Warnings) == 0 {
		t.Fatal("partial one-year claim must require review", err)
	}
	check(t, "inconsistent claim", s.TotalAllowance, 0)
}

func TestCAReviewInconsistentHistory(t *testing.T) {
	a := Asset{Description: "Machine", Cost: d(10000), AcquiredYA: 2024, Method: CA2Year, ClaimsConfirmedThroughYA: 2025, ClaimHistory: []CAClaim{{YA: 2024, Amount: d(2500)}, {YA: 2025, Amount: d(7500)}}}
	s, err := ComputeCA([]Asset{a}, 2026)
	if err != nil || len(s.Warnings) == 0 {
		t.Fatal("swapped two-year claims must require review", err)
	}
	a.Method = CA1Year
	a.ClaimHistory = []CAClaim{{YA: 2024, Amount: d(2000)}}
	a.DisposedYA, a.DisposalProceeds = 2026, d(5000)
	s, err = ComputeCA([]Asset{a}, 2026)
	if err != nil || len(s.Warnings) == 0 {
		t.Fatal("disposal history must require review", err)
	}
}

func TestCAValidatedHistoryAndDisposal(t *testing.T) {
	a := Asset{Description: "Computer", Cost: d(9000), AcquiredYA: 2024, Method: CA1Year, ClaimsConfirmedThroughYA: 2025, ClaimHistory: []CAClaim{{YA: 2024, Amount: d(9000)}}, DisposedYA: 2026, DisposalProceeds: d(1000)}
	s, err := ComputeCA([]Asset{a}, 2026)
	if err != nil || len(s.Warnings) != 0 {
		t.Fatalf("valid disposal: %v %v", err, s.Warnings)
	}
	check(t, "balancing charge", s.BalancingCharge, d(1000))
	a.Method, a.DisposedYA, a.DisposalProceeds = CA3Year, 0, 0
	a.ClaimHistory = []CAClaim{{YA: 2024, Amount: d(1000)}, {YA: 2025, Amount: d(5000)}}
	s, err = ComputeCA([]Asset{a}, 2026)
	if err != nil || len(s.Warnings) == 0 {
		t.Fatal("offsetting invalid installments must require review", err)
	}
}

func TestCANormalSection19History(t *testing.T) {
	a := Asset{Description: "Machine", Cost: d(12000), AcquiredYA: 2024, Method: CAS19, WorkingLife: 6, ClaimsConfirmedThroughYA: 2024, ClaimHistory: []CAClaim{{YA: 2024, Amount: d(4000)}}}
	s, err := ComputeCA([]Asset{a}, 2025)
	if err != nil || len(s.Warnings) != 0 {
		t.Fatalf("normal section 19 history: %v %v", err, s.Warnings)
	}
	check(t, "annual allowance", s.TotalAllowance, d(1600))
	a.ClaimHistory = []CAClaim{{YA: 2024, Amount: d(12000)}}
	a.DisposedYA, a.DisposalProceeds = 2025, d(6000)
	s, err = ComputeCA([]Asset{a}, 2025)
	if err != nil || len(s.Warnings) == 0 {
		t.Fatal("invalid fully claimed section 19 history must require review", err)
	}
}

func TestCADisposalWithoutPriorClaims(t *testing.T) {
	a := Asset{Description: "Machine", Cost: d(9000), AcquiredYA: 2024, Method: CA3Year, ClaimsConfirmedThroughYA: 2025, DisposedYA: 2026, DisposalProceeds: d(6000)}
	s, err := ComputeCA([]Asset{a}, 2026)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "balancing allowance", s.BalancingAllowance, 0)
	check(t, "balancing charge", s.BalancingCharge, 0)
}

func TestCALowValueCap(t *testing.T) {
	var assets []Asset
	for i := 0; i < 8; i++ {
		assets = append(assets, Asset{Description: "Item", Cost: d(5000), AcquiredYA: 2026, Method: CALowVal})
	}
	s, _ := ComputeCA(assets, 2026)
	// Six fit within the cap; the rest require an explicitly recorded election.
	check(t, "capped", s.TotalAllowance, d(30000))
	if len(s.Warnings) == 0 || s.Lines[6].Asset.Method != CALowVal {
		t.Fatal("overflow must not silently change the elected method")
	}
	assets[6].Method = CA3Year
	s, _ = ComputeCA(assets[:7], 2026)
	check(t, "recorded three-year election", s.TotalAllowance, d(30000)+(d(5000)+1)/3)
	assets[6].ClaimsConfirmedThroughYA = 2026
	assets[6].ClaimHistory = []CAClaim{{YA: 2026, Amount: (d(5000) + 1) / 3}}
	s, _ = ComputeCA(assets[:7], 2027)
	check(t, "following year", s.Lines[6].Allowance, (d(5000)+1)/3)
}

func TestCALowValueCapExcludesDisposedAsset(t *testing.T) {
	var assets []Asset
	for i := 0; i < 7; i++ {
		assets = append(assets, Asset{Description: "Item", Cost: d(5000), AcquiredYA: 2026, Method: CALowVal})
	}
	assets[0].DisposedYA = 2026
	s, err := ComputeCA(assets, 2026)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "cap excludes disposal", s.TotalAllowance, d(30000))
	if len(s.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", s.Warnings)
	}
}

func TestCAS19(t *testing.T) {
	a := Asset{Description: "Machine", Cost: d(10000), AcquiredYA: 2025, Method: CAS19, WorkingLife: 4}
	var total int64
	for ya := 2025; ya <= 2029; ya++ {
		s, _ := ComputeCA([]Asset{a}, ya)
		total += int64(s.TotalAllowance)
	}
	if total != int64(d(10000)) {
		t.Errorf("S19 allowances must total cost, got %d", total)
	}
}

func TestCATwoYear(t *testing.T) {
	// 75%/25% for assets acquired in the basis period for YA2021, YA2022 or YA2024 (Budget 2023).
	a := Asset{Description: "Machine", Cost: d(100000), AcquiredYA: 2024, Method: CA2Year}
	s, _ := ComputeCA([]Asset{a}, 2024)
	check(t, "year 1", s.TotalAllowance, d(75000))
	s, _ = ComputeCA([]Asset{a}, 2025)
	check(t, "year 2", s.TotalAllowance, d(25000))
	s, _ = ComputeCA([]Asset{a}, 2026)
	check(t, "year 3", s.TotalAllowance, 0)
	if why := ValidateMethod(Asset{Description: "X", Cost: d(1000), AcquiredYA: 2023, Method: CA2Year}); why == "" {
		t.Error("2-year write-off must be rejected for YA2023 acquisitions")
	}
	if why := ValidateMethod(Asset{Description: "X", Cost: d(6000), AcquiredYA: 2026, Method: CALowVal}); why == "" {
		t.Error("low-value write-off must be rejected above $5,000 per asset")
	}
}

func TestCAS19FirstYear(t *testing.T) {
	// 20% initial allowance plus first annual allowance of the 80% over the working life.
	a := Asset{Description: "Machine", Cost: d(100000), AcquiredYA: 2025, Method: CAS19, WorkingLife: 8}
	s, _ := ComputeCA([]Asset{a}, 2025)
	check(t, "IA+AA", s.TotalAllowance, d(20000)+d(10000))
	s, _ = ComputeCA([]Asset{a}, 2026)
	check(t, "AA", s.TotalAllowance, d(10000))
}
