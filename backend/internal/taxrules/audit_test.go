package taxrules

import (
	"strings"
	"testing"

	"tidal/backend/internal/money"
)

// C1: an asset with no allowance gets no balancing allowance when it is sold.
func TestNoAllowanceAssetDisposal(t *testing.T) {
	car := Asset{Description: "S-plate car", Cost: d(100000), AcquiredYA: 2024, Method: CANone, DisposedYA: 2026, DisposalProceeds: d(60000)}
	s, _ := ComputeCA([]Asset{car}, 2026)
	check(t, "balancing allowance", s.BalancingAllowance, 0)
	check(t, "balancing charge", s.BalancingCharge, 0)
}

// C2: hire-purchase assets are not given the full allowance up front.
func TestHirePurchaseNotClaimedInFull(t *testing.T) {
	pc := Asset{Description: "Computer on HP", Cost: d(2000), AcquiredYA: 2026, Method: CA1Year, HirePurchase: true}
	s, _ := ComputeCA([]Asset{pc}, 2026)
	check(t, "allowance", s.TotalAllowance, 0)
	if len(s.Warnings) == 0 || !strings.Contains(s.Warnings[0], "hire purchase") {
		t.Fatalf("no hire-purchase warning: %v", s.Warnings)
	}
}

// C17: invalid elections earn nothing rather than an accelerated allowance.
func TestInvalidElectionsNotClaimed(t *testing.T) {
	big := Asset{Description: "Big item", Cost: d(10000), AcquiredYA: 2026, Method: CALowVal}
	twoYear := Asset{Description: "Machine", Cost: d(9000), AcquiredYA: 2026, Method: CA2Year}
	s, _ := ComputeCA([]Asset{big, twoYear}, 2026)
	check(t, "allowance", s.TotalAllowance, 0)
	if ValidateMethod(big) == "" || ValidateMethod(twoYear) == "" {
		t.Fatal("invalid elections accepted")
	}
}

// C18: the historical elected method must not depend on current asset ordering.
func TestLowValueCapKeepsHistoryConsistent(t *testing.T) {
	assets := []Asset{
		{Description: "Low-value item", Cost: d(5000), AcquiredYA: 2026, Method: CALowVal, ClaimsConfirmedThroughYA: 2026, ClaimHistory: []CAClaim{{YA: 2026, Amount: d(5000)}}},
		{Description: "Three-year item", Cost: d(3000), AcquiredYA: 2026, Method: CA3Year, ClaimsConfirmedThroughYA: 2026, ClaimHistory: []CAClaim{{YA: 2026, Amount: d(1000)}}},
	}
	s, err := ComputeCA(assets, 2027)
	if err != nil {
		t.Fatal(err)
	}
	check(t, "second year", s.TotalAllowance, d(1000))
	if len(s.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", s.Warnings)
	}
}

// C3: IRAS box 5 excludes blocked purchases and includes Major Exporter Scheme imports.
func TestGSTBox5BlockedAndMES(t *testing.T) {
	r, _ := ComputeGSTF5(GSTInput{PeriodStart: date(2026, 1, 1), PeriodEnd: date(2026, 3, 31), Lines: []GSTLine{
		{Kind: "purchase", Code: "BL", Net: d(1000), GST: d(90)},
		{Kind: "purchase", Code: "ME", Net: d(10000)},
	}})
	for _, b := range r.Boxes {
		if b.Box == "5" && b.Value != d(10000) {
			t.Fatalf("box 5 = %v, want 10,000", b.Value)
		}
		if b.Box == "9" && b.Value != d(10000) {
			t.Fatalf("box 9 = %v, want 10,000", b.Value)
		}
	}
}

func TestGSTRejectsCodeDirectionAndAmountConflicts(t *testing.T) {
	period := GSTInput{PeriodStart: date(2026, 1, 1), PeriodEnd: date(2026, 3, 31)}
	for _, line := range []GSTLine{
		{Kind: "sale", Code: "TX", Net: d(100), GST: d(9)},
		{Kind: "purchase", Code: "SR", Net: d(100), GST: d(9)},
		{Kind: "purchase", Code: "TX", Net: d(100), GST: -d(9)},
		{Kind: "sale", Code: "SR", Net: 0, GST: d(9)},
	} {
		period.Lines = []GSTLine{line}
		if _, err := ComputeGSTF5(period); err == nil {
			t.Errorf("invalid GST coding accepted: %+v", line)
		}
	}
}

// C4: unabsorbed capital allowances reduce other income (resident aged 40, YA2026).
func TestPersonalCAOffsetsOtherIncome(t *testing.T) {
	r, err := ComputePIT(PITInput{YA: 2026, Age: 40, Resident: true, Revenue: d(1), CapitalAllowances: d(10000), EmploymentIncome: d(50000)})
	if err != nil {
		t.Fatal(err)
	}
	check(t, "total income", r.TotalIncome, d(40000))
	check(t, "CA carried forward", r.CACF, 0)
	check(t, "tax", r.GrossTax, d(515))
}

// C5: from YA2026 compulsory MediSave is relieved in full.
func TestCPFReliefYA2026(t *testing.T) {
	base := PITInput{YA: 2026, Age: 40, Resident: true, Revenue: d(30000), GrossProfit: d(30000), AllowableExpenses: d(20000)}
	in := base
	in.Reliefs = []Item{{Label: "cpf_medisave_compulsory", Amount: d(4200)}}
	r, _ := ComputePIT(in)
	check(t, "compulsory relief", relief(r, "cpf_medisave_compulsory"), d(4200))
	// Voluntary contributions only use what is left of 37% of net trade income (3,700 - 3,000 = 700).
	in.Reliefs = []Item{{Label: "cpf_medisave_compulsory", Amount: d(3000)}, {Label: "cpf_voluntary", Amount: d(2000)}}
	r, _ = ComputePIT(in)
	check(t, "voluntary relief", relief(r, "cpf_voluntary"), d(700))
	// Before YA2026 the combined amount is capped, and a zero limit means no relief.
	old := base
	old.YA = 2025
	old.GrossProfit = old.AllowableExpenses
	old.Reliefs = []Item{{Label: "cpf_medisave_compulsory", Amount: d(4200)}}
	r, _ = ComputePIT(old)
	check(t, "pre-2026 with no profit", relief(r, "cpf_self_employed"), 0)
}

func TestPITRepeatedReliefsShareOneLimit(t *testing.T) {
	r, err := ComputePIT(PITInput{YA: 2026, Age: 40, Resident: true, Reliefs: []Item{
		{Label: "srs", Amount: d(15300)}, {Label: "srs", Amount: d(15300)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if r.TotalReliefs != d(15300) || len(r.Reliefs) != 1 || r.Reliefs[0].Amount != d(15300) {
		t.Fatalf("duplicate SRS rows must aggregate before the per-type cap: %+v", r)
	}
}

func TestPITNonResidentTradeIncomeUsesNonResidentRate(t *testing.T) {
	r, err := ComputePIT(PITInput{YA: 2026, Resident: false, Revenue: d(250000), GrossProfit: d(200000)})
	if err != nil {
		t.Fatal(err)
	}
	if r.NetTax != d(48000) || r.TotalReliefs != 0 {
		t.Fatalf("non-resident trade income should use 24%% without personal reliefs: %+v", r)
	}
}

func TestPITNonResidentMixedIncomeIsRefused(t *testing.T) {
	_, err := ComputePIT(PITInput{YA: 2026, Resident: false, GrossProfit: d(200000), EmploymentIncome: d(10000)})
	if err == nil {
		t.Fatal("mixed non-resident income needs separate source treatment")
	}
}

func TestPITNegativeReliefIsRefused(t *testing.T) {
	_, err := ComputePIT(PITInput{YA: 2026, Resident: true, Reliefs: []Item{{Label: "srs", Amount: -d(100)}}})
	if err == nil {
		t.Fatal("negative relief must be refused")
	}
}

func relief(r PITResult, code string) (total money.Cents) {
	for _, it := range r.Reliefs {
		if it.Label == code {
			total += it.Amount
		}
	}
	return total
}

// C19: Form B statement format and certified accounts thresholds.
func TestFormBThresholds(t *testing.T) {
	for _, c := range []struct {
		revenue   int64
		lines     int
		certified bool
	}{{200000, 2, false}, {200001, 4, false}, {500000, 4, true}} {
		r, _ := ComputePIT(PITInput{YA: 2026, Age: 40, Resident: true, Revenue: d(c.revenue), GrossProfit: d(c.revenue), AllowableExpenses: d(c.revenue)})
		cert := strings.Contains(strings.Join(r.Warnings, " "), "certified")
		if r.StatementLines != c.lines || cert != c.certified {
			t.Errorf("revenue %d: %d lines, certified warning %v", c.revenue, r.StatementLines, cert)
		}
	}
}
