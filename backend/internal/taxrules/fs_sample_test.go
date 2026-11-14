package taxrules

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"tidal/backend/internal/money"
)

// Published-sample checks: figures come from BDO Singapore's illustrative annual report for FY2024
// (testdata/bdo_sg_illustrative_fy2024.json). The notes below say what each check can and cannot prove,
// because a consolidated group statement is not a single company's tax computation.

type fsSample struct {
	FinancialYearEnd string `json:"financialYearEnd"`
	YearOfAssessment int    `json:"yearOfAssessment"`
	IncomeStatement  struct {
		Revenue2024                     int64 `json:"revenue2024"`
		Revenue2023                     int64 `json:"revenue2023"`
		ProfitBeforeTaxContinuing2024   int64 `json:"profitBeforeTaxContinuing2024"`
		ProfitBeforeTaxDiscontinued2024 int64 `json:"profitBeforeTaxDiscontinued2024"`
		IncomeTaxExpenseContinuing2024  int64 `json:"incomeTaxExpenseContinuing2024"`
		ShareOfAssociatesNetOfTax2024   int64 `json:"shareOfAssociatesNetOfTax2024"`
		ShareOfJointVentureNetOfTax2024 int64 `json:"shareOfJointVentureNetOfTax2024"`
	} `json:"incomeStatement"`
	TaxNote struct {
		StatutoryRateBP          int64 `json:"statutoryRateBP"`
		BaseForStatutoryRate2024 int64 `json:"baseForStatutoryRate2024"`
		TaxAtStatutoryRate2024   int64 `json:"taxAtStatutoryRate2024"`
	} `json:"taxNote"`
	BalanceSheet struct {
		TotalAssets2024 int64 `json:"totalAssets2024"`
		TotalAssets2023 int64 `json:"totalAssets2023"`
	} `json:"balanceSheet"`
}

func loadFSSample(t *testing.T) fsSample {
	t.Helper()
	raw, err := os.ReadFile("testdata/bdo_sg_illustrative_fy2024.json")
	if err != nil {
		t.Fatal(err)
	}
	var s fsSample
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func cents(v int64) money.Cents { return money.Cents(v * 100) }

// The note's rate-base line: PBT (continuing + discontinued) less associates less the joint venture.
func TestSampleFSRateBaseAgreesWithTaxNote(t *testing.T) {
	s := loadFSSample(t)
	want := cents(s.TaxNote.BaseForStatutoryRate2024)
	pbt := cents(s.IncomeStatement.ProfitBeforeTaxContinuing2024 + s.IncomeStatement.ProfitBeforeTaxDiscontinued2024)
	r, err := ComputeCIT(CITInput{
		YA:                 s.YearOfAssessment,
		Revenue:            cents(s.IncomeStatement.Revenue2024),
		NetProfitBeforeTax: pbt,
		NonTaxableIncome: []Item{
			{Label: "Share of profits of associates, net of tax", Amount: cents(s.IncomeStatement.ShareOfAssociatesNetOfTax2024)},
			{Label: "Share of profit of a joint venture, net of tax", Amount: cents(s.IncomeStatement.ShareOfJointVentureNetOfTax2024)},
		},
		OnlyIncomeAt17: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	check(t, "rate base (Form C-S line 5)", r.AdjustedBeforeOtherDed, want)

	// 17% x $10,598k = $1,801.66k; the note shows $1,801k because the face rounds to $'000.
	got := r.AdjustedBeforeOtherDed.MulRate(s.TaxNote.StatutoryRateBP)
	if diff := got - cents(s.TaxNote.TaxAtStatutoryRate2024); diff > money.Cents(100000) || diff < money.Cents(-100000) {
		t.Errorf("17%% on the rate base = %s, note says %s", got, cents(s.TaxNote.TaxAtStatutoryRate2024))
	}
}

// Revenue above $5m puts the sample company on Form C, not Form C-S.
func TestSampleFSFormChoiceFromPublishedRevenue(t *testing.T) {
	s := loadFSSample(t)
	r, err := ComputeCIT(CITInput{YA: s.YearOfAssessment, Revenue: cents(s.IncomeStatement.Revenue2024), OnlyIncomeAt17: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.FormType != "C" || len(r.FormCSchedules) == 0 {
		t.Errorf("revenue $%d should require Form C with a schedule note, got %s", s.IncomeStatement.Revenue2024, r.FormType)
	}
}

// Revenue and total assets both exceed the $10m small-company limits, so ACRA filing relief cannot apply.
func TestSampleFSIsNotASmallCompany(t *testing.T) {
	s := loadFSSample(t)
	fye, err := time.Parse("2006-01-02", s.FinancialYearEnd)
	if err != nil {
		t.Fatal(err)
	}
	r := computeACRA(ACRAInput{
		FYE: fye,
		Current: SizeTest{
			Revenue:     cents(s.IncomeStatement.Revenue2024),
			TotalAssets: cents(s.BalanceSheet.TotalAssets2024),
			Employees:   100,
		},
	})
	if r.SmallCompany {
		t.Error("revenue and total assets both above $10m must not qualify as a small company")
	}
}

// FY2024 periods carry the 9% GST rate that applies from 1 January 2024.
func TestSampleFSPeriodUsesNinePercentGST(t *testing.T) {
	s := loadFSSample(t)
	end, err := time.Parse("2006-01-02", s.FinancialYearEnd)
	if err != nil {
		t.Fatal(err)
	}
	if got := GSTRateBP(end); got != 900 {
		t.Errorf("GST rate at %s = %d bp, want 900", s.FinancialYearEnd, got)
	}
	if got := GSTRateBP(end.AddDate(-1, 0, 0)); got != 800 {
		t.Errorf("GST rate one year earlier = %d bp, want 800", got)
	}
}
