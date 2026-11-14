package taxrules

import (
	"strings"
	"testing"

	"tidal/backend/internal/money"
)

func d(v int64) money.Cents { return money.Cents(v * 100) }

// Every case below was run through the IRAS YA2026 Basic Corporate Income Tax Calculator
// (btc-2026-form-cs.xlsm, recalculated in LibreOffice) on 2026-10-18 and matches it to the cent.
func TestCITPartialExemptionWithRebate(t *testing.T) {
	r, err := ComputeCIT(CITInput{
		YA: 2026, Revenue: d(1284560), NetProfitBeforeTax: d(108600), OnlyIncomeAt17: true, LocalEmployee: true,
		NonDeductible: []Item{{Label: "Depreciation", Amount: d(36210)}, {Label: "Donations", Amount: d(5000)}, {Label: "S-plate car expenses", Amount: d(9300)}},
		CurrentYearCA: d(12000), IPCDonations: d(5000),
	})
	if err != nil {
		t.Fatal(err)
	}
	check(t, "adjusted before CA", r.AdjustedBeforeCA, d(159110))
	check(t, "chargeable income", r.ChargeableIncome, d(134610))
	check(t, "exempt", r.ExemptAmount, d(69805))
	check(t, "gross tax", r.GrossTax, 1101685)
	check(t, "rebate before grant", r.RebateBeforeGrant, 550843)
	check(t, "net rebate", r.NetRebate, 350843)
	check(t, "net tax", r.NetTaxPayable, 750842)
	if r.FormType != "C-S" {
		t.Errorf("form %s", r.FormType)
	}
	if r.ECIWaived {
		t.Error("ECI must not be waived when chargeable income is positive")
	}
}

func TestCITStartUpExemptionLite(t *testing.T) {
	r, err := ComputeCIT(CITInput{YA: 2026, Revenue: d(150000), NetProfitBeforeTax: d(250000), StartUpExemption: true, LocalEmployee: true, OnlyIncomeAt17: true})
	if err != nil {
		t.Fatal(err)
	}
	check(t, "exempt", r.ExemptAmount, d(125000))
	check(t, "gross tax", r.GrossTax, d(21250))
	check(t, "net tax", r.NetTaxPayable, d(12625))
	if r.FormType != "C-S Lite" {
		t.Errorf("form %s", r.FormType)
	}
}

func TestCITLossesAndCarryForwards(t *testing.T) {
	r, err := ComputeCIT(CITInput{YA: 2025, Revenue: d(400000), NetProfitBeforeTax: d(-20000), OnlyIncomeAt17: true,
		CurrentYearCA: d(5000), InterestIncome: d(3000), UnutilisedLossesBF: d(10000), IPCDonations: d(1000)})
	if err != nil {
		t.Fatal(err)
	}
	check(t, "chargeable income", r.ChargeableIncome, 0)
	check(t, "CA c/f", r.UnutilisedCACF, d(2000))
	check(t, "losses c/f", r.UnutilisedLossesCF, d(30000))
	check(t, "donations c/f", r.UnutilisedDonationsCF, d(2500))
	check(t, "total before donations", r.TotalBeforeDonations, d(-32000))
	if !r.ECIWaived {
		t.Error("ECI should be waived: revenue <= $5m and nil chargeable income")
	}
}

func TestCITBalancingChargeExceedsAllowances(t *testing.T) {
	r, err := ComputeCIT(CITInput{YA: 2026, Revenue: d(900000), NetProfitBeforeTax: d(50000), OnlyIncomeAt17: true, BalancingCharge: d(8000), CurrentYearCA: d(3000)})
	if err != nil {
		t.Fatal(err)
	}
	check(t, "chargeable income", r.ChargeableIncome, d(55000))
	check(t, "exempt", r.ExemptAmount, d(30000))
	check(t, "net tax", r.NetTaxPayable, d(2125))
	check(t, "CA c/f", r.UnutilisedCACF, 0)
}

func TestCITDonationOrdering(t *testing.T) {
	// Income 10,000 against b/f donations 4,000, overseas 3,000 and current IPC 2,000 x 2.5 = 5,000.
	r, err := ComputeCIT(CITInput{YA: 2026, Revenue: d(500000), NetProfitBeforeTax: d(10000), OnlyIncomeAt17: true,
		UnutilisedDonationsBF: d(4000), OverseasDonations: d(3000), IPCDonations: d(2000)})
	if err != nil {
		t.Fatal(err)
	}
	check(t, "chargeable", r.ChargeableIncome, 0)
	check(t, "donations c/f", r.UnutilisedDonationsCF, d(2000)) // IRAS: Donation - (F102 - UDbf - overseas)
	check(t, "after donations", r.TotalAfterDonations, d(-2000))
}

func TestCITFormC(t *testing.T) {
	r, _ := ComputeCIT(CITInput{YA: 2026, Revenue: d(6000000), NetProfitBeforeTax: d(1), OnlyIncomeAt17: true})
	if r.FormType != "C" {
		t.Errorf("form %s", r.FormType)
	}
	if len(r.FormCSchedules) != 1 || !strings.Contains(r.FormCSchedules[0], "Form C-format tax computation") {
		t.Fatalf("Form C revenue case missing format schedule: %v", r.FormCSchedules)
	}
	r, _ = ComputeCIT(CITInput{YA: 2026, Revenue: d(100), NetProfitBeforeTax: d(1), ClaimsGroupRelief: true, ClaimsCarryBack: true, ClaimsInvestmentAllowance: true, ClaimsForeignTaxCredit: true})
	if r.FormType != "C" {
		t.Errorf("group relief must force Form C, got %s", r.FormType)
	}
	if len(r.FormCSchedules) != 5 {
		t.Fatalf("expected non-17%% plus four specialist schedules; got %v", r.FormCSchedules)
	}
	for _, want := range []string{"concessionary rates", "preceding YA", "Form GR-A", "Investment Allowance", "Foreign tax credit"} {
		found := false
		for _, schedule := range r.FormCSchedules {
			if strings.Contains(schedule, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing schedule %q in %v", want, r.FormCSchedules)
		}
	}
	cs, _ := ComputeCIT(CITInput{YA: 2026, Revenue: d(400000), NetProfitBeforeTax: d(1), OnlyIncomeAt17: true})
	if cs.FormType == "C" || len(cs.FormCSchedules) != 0 {
		t.Fatalf("supported Form C-S case unexpectedly reports missing Form C schedules: %s %v", cs.FormType, cs.FormCSchedules)
	}
}

func TestCITNoRebateYA2027(t *testing.T) {
	r, _ := ComputeCIT(CITInput{YA: 2027, Revenue: d(1000000), NetProfitBeforeTax: d(300000), OnlyIncomeAt17: true, LocalEmployee: true})
	check(t, "exempt", r.ExemptAmount, d(102500))
	check(t, "net tax", r.NetTaxPayable, d(197500).MulRate(1700))
}

func TestTiered(t *testing.T) {
	check(t, "pte small", Tiered(d(8000), pte2020), d(6000))
	check(t, "pte exact cap", Tiered(d(200000), pte2020), d(102500))
	check(t, "pte over cap", Tiered(d(200001), pte2020), d(102500))
	check(t, "pte cap", Tiered(d(5000000), pte2020), d(102500))
	check(t, "sute exact cap", Tiered(d(200000), sute2020), d(125000))
	check(t, "sute over cap", Tiered(d(200001), sute2020), d(125000))
}

// Cases below are the IRAS-published worked examples ("Examples on computing
// the amount of tax exemption for companies", iras.gov.sg): chargeable income
// is fed straight in as accounting profit with no adjustments, and every
// intermediate figure must match the published answer to the cent.
func TestCITIRASStartUpYA2020(t *testing.T) {
	r, err := ComputeCIT(CITInput{YA: 2020, Revenue: d(1000000), NetProfitBeforeTax: d(250000), StartUpExemption: true, OnlyIncomeAt17: true})
	if err != nil {
		t.Fatal(err)
	}
	check(t, "exempt", r.ExemptAmount, d(125000))
	check(t, "after exempt", r.ChargeableAfterExempt, d(125000))
	check(t, "gross tax", r.GrossTax, 2125000)
	check(t, "net rebate", r.NetRebate, 531250)
	check(t, "net tax", r.NetTaxPayable, 1593750)
}

func TestCITIRASStartUpYA2021(t *testing.T) {
	r, err := ComputeCIT(CITInput{YA: 2021, Revenue: d(1000000), NetProfitBeforeTax: d(230000), StartUpExemption: true, OnlyIncomeAt17: true})
	if err != nil {
		t.Fatal(err)
	}
	check(t, "exempt", r.ExemptAmount, d(125000))
	check(t, "after exempt", r.ChargeableAfterExempt, d(105000))
	check(t, "gross tax", r.GrossTax, 1785000)
	check(t, "net tax", r.NetTaxPayable, 1785000)
}

func TestCITIRASPartialExemptionYA2021(t *testing.T) {
	r, err := ComputeCIT(CITInput{YA: 2021, Revenue: d(2000000), NetProfitBeforeTax: d(600000), OnlyIncomeAt17: true})
	if err != nil {
		t.Fatal(err)
	}
	check(t, "exempt", r.ExemptAmount, d(102500))
	check(t, "after exempt", r.ChargeableAfterExempt, d(497500))
	check(t, "gross tax", r.GrossTax, 8457500)
	check(t, "net tax", r.NetTaxPayable, 8457500)
}

func TestCITIRASPartialExemptionYA2022(t *testing.T) {
	r, err := ComputeCIT(CITInput{YA: 2022, Revenue: d(1000000), NetProfitBeforeTax: d(290000), OnlyIncomeAt17: true})
	if err != nil {
		t.Fatal(err)
	}
	check(t, "exempt", r.ExemptAmount, d(102500))
	check(t, "after exempt", r.ChargeableAfterExempt, d(187500))
	check(t, "gross tax", r.GrossTax, 3187500)
	check(t, "net tax", r.NetTaxPayable, 3187500)
}

// Full adjustment flow from a published walkthrough: profit 260,000 plus
// 25,000 depreciation and 5,000 disallowed expenses, less 40,000 capital
// allowances, gives chargeable income 250,000 and tax 25,075 (no rebate year).
func TestCITAdjustmentFlowToChargeableIncome(t *testing.T) {
	r, err := ComputeCIT(CITInput{YA: 2022, Revenue: d(1800000), NetProfitBeforeTax: d(260000), OnlyIncomeAt17: true,
		NonDeductible: []Item{{Label: "Depreciation", Amount: d(25000)}, {Label: "Disallowed expenses", Amount: d(5000)}},
		CurrentYearCA: d(40000)})
	if err != nil {
		t.Fatal(err)
	}
	check(t, "adjusted before CA", r.AdjustedBeforeCA, d(290000))
	check(t, "chargeable income", r.ChargeableIncome, d(250000))
	check(t, "exempt", r.ExemptAmount, d(102500))
	check(t, "after exempt", r.ChargeableAfterExempt, d(147500))
	check(t, "gross tax", r.GrossTax, 2507500)
	check(t, "net tax", r.NetTaxPayable, 2507500)
}

func check(t *testing.T, name string, got, want money.Cents) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %s, want %s", name, got, want)
	}
}
