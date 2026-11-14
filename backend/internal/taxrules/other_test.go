package taxrules

import (
	"strings"
	"testing"
	"time"

	"tidal/backend/internal/money"
)

func date(y int, m time.Month, dd int) time.Time { return time.Date(y, m, dd, 0, 0, 0, 0, time.UTC) }

func TestPITProgressive(t *testing.T) {
	// IRAS table YA2024+: tax on first $1,000,000 is $199,150.
	y, _ := PITFor(2026)
	check(t, "1m", progressive(d(1000000), y.Bands), d(199150))
	check(t, "40k", progressive(d(40000), y.Bands), d(550))
	check(t, "320k", progressive(d(320000), y.Bands), d(44550))
	check(t, "zero", progressive(d(15000), y.Bands), 0)
}

func TestPITSoleProprietor(t *testing.T) {
	r, err := ComputePIT(PITInput{YA: 2025, Age: 40, Resident: true, Revenue: d(180000), GrossProfit: d(120000), AllowableExpenses: d(40000),
		Reliefs: []Item{{Label: "cpf_self_employed", Amount: d(8000)}, {Label: "nsman", Amount: d(3000)}}})
	if err != nil {
		t.Fatal(err)
	}
	// Assessable 80,000 - reliefs (1,000 + 8,000 + 3,000) = 68,000 -> 550 + 7% x 28,000 = 2,510; YA2025 rebate 60% capped $200.
	check(t, "chargeable", r.ChargeableIncome, d(68000))
	check(t, "gross", r.GrossTax, d(2510))
	check(t, "rebate", r.Rebate, d(200))
	check(t, "net", r.NetTax, d(2310))
	// IRAS: revenue of $200,000 or less is reported with the 2-line statement.
	if r.FormType != "B" || r.StatementLines != 2 {
		t.Errorf("form %s statement lines %v", r.FormType, r.StatementLines)
	}
}

func TestPITAgeReliefAndCap(t *testing.T) {
	// Age 62: earned income relief $8,000 (IRAS: 60+). YA2024 rebate 50% capped $200.
	r, err := ComputePIT(PITInput{YA: 2024, Age: 62, Resident: true, EmploymentIncome: d(100000)})
	if err != nil {
		t.Fatal(err)
	}
	check(t, "EIR 60+", r.EarnedIncomeRelief, d(8000))
	// Chargeable 92,000 -> 3,350 + 11.5% x 12,000 = 4,730; rebate min(50%, 200) = 200.
	check(t, "chargeable", r.ChargeableIncome, d(92000))
	check(t, "gross", r.GrossTax, d(4730))
	check(t, "rebate", r.Rebate, d(200))
	check(t, "net", r.NetTax, d(4530))
	// Reliefs above $80,000 are capped (IRAS personal relief cap).
	r, _ = ComputePIT(PITInput{YA: 2024, Age: 40, Resident: true, EmploymentIncome: d(500000),
		Reliefs: []Item{{Label: "parent", Amount: d(50000)}, {Label: "qcr", Amount: d(40000)}}})
	check(t, "capped reliefs", r.TotalReliefs, d(80000))
	if len(r.Warnings) == 0 {
		t.Error("expected cap warning")
	}
	// Age 57: $6,000.
	r, _ = ComputePIT(PITInput{YA: 2024, Age: 57, Resident: true, EmploymentIncome: d(100000)})
	check(t, "EIR 55-59", r.EarnedIncomeRelief, d(6000))
}

func TestGSTF5(t *testing.T) {
	r, err := ComputeGSTF5(GSTInput{PeriodStart: date(2026, 7, 1), PeriodEnd: date(2026, 9, 30), Revenue: d(130000), Lines: []GSTLine{
		{Kind: "sale", Code: "SR", Net: d(100000), GST: d(9000)},
		{Kind: "sale", Code: "ZR", Net: d(30000)},
		{Kind: "purchase", Code: "TX", Net: d(40000), GST: d(3600)},
		{Kind: "purchase", Code: "BL", Net: d(1000), GST: d(90)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	box := func(b string) int64 {
		for _, x := range r.Boxes {
			if x.Box == b {
				return int64(x.Value)
			}
		}
		return -1
	}
	if box("1") != int64(d(100000)) || box("4") != int64(d(130000)) || box("5") != int64(d(40000)) || box("7") != int64(d(3600)) || box("8") != int64(d(5400)) {
		t.Errorf("boxes wrong: %+v", r.Boxes)
	}
	if !r.DueDate.Equal(date(2026, 10, 31)) {
		t.Errorf("due %v", r.DueDate)
	}
	if r.RateBP != 900 {
		t.Errorf("rate %d", r.RateBP)
	}
}

func TestGSTRateHistory(t *testing.T) {
	// IRAS: 7% before 2023, 8% from 1 Jan 2023, 9% from 1 Jan 2024.
	check(t, "2022", money.Cents(GSTRateBP(date(2022, 12, 31))), 700)
	check(t, "2023", money.Cents(GSTRateBP(date(2023, 1, 1))), 800)
	check(t, "2023 end", money.Cents(GSTRateBP(date(2023, 12, 31))), 800)
	check(t, "2024", money.Cents(GSTRateBP(date(2024, 1, 1))), 900)
}

func TestGSTF5AllCodes(t *testing.T) {
	// Box mapping per the IRAS e-Tax Guide "How do I prepare my GST return":
	// SR/DS/SRRC -> boxes 1+6; ZR -> 2; ES33/ESN33 -> 3; TX/IM -> 5+7; TXRC -> 1+5+6+7 and 14 (see TestGSTReverseChargePurchase);
	// ZP -> 5 only; BL excluded; ME -> 5+9; OS/EP/OP/NR ignored.
	r, err := ComputeGSTF5(GSTInput{PeriodStart: date(2026, 1, 1), PeriodEnd: date(2026, 3, 31), Revenue: d(200000), Lines: []GSTLine{
		{Kind: "sale", Code: "SR", Net: d(100000), GST: d(9000)},
		{Kind: "sale", Code: "DS", Net: d(1000), GST: d(90)},
		{Kind: "sale", Code: "SRRC", Net: d(5000), GST: d(450)},
		{Kind: "sale", Code: "ZR", Net: d(30000)},
		{Kind: "sale", Code: "ES33", Net: d(2000)},
		{Kind: "sale", Code: "ESN33", Net: d(3000)},
		{Kind: "sale", Code: "OS", Net: d(99999)},
		{Kind: "purchase", Code: "TX", Net: d(40000), GST: d(3600)},
		{Kind: "purchase", Code: "IM", Net: d(10000), GST: d(900)},
		{Kind: "purchase", Code: "TXRC", Net: d(5000), GST: d(450)},
		{Kind: "purchase", Code: "ZP", Net: d(7000)},
		{Kind: "purchase", Code: "BL", Net: d(1000), GST: d(90)},
		{Kind: "purchase", Code: "ME", Net: d(20000)},
		{Kind: "purchase", Code: "EP", Net: d(88888)},
		{Kind: "purchase", Code: "OP", Net: d(77777)},
		{Kind: "purchase", Code: "NR", Net: d(66666)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	box := func(b string) int64 {
		for _, x := range r.Boxes {
			if x.Box == b {
				return int64(x.Value)
			}
		}
		return -1
	}
	check(t, "box1", money.Cents(box("1")), d(111000))
	check(t, "box2", money.Cents(box("2")), d(30000))
	check(t, "box3", money.Cents(box("3")), d(5000))
	check(t, "box4", money.Cents(box("4")), d(146000))
	check(t, "box5", money.Cents(box("5")), d(82000))
	check(t, "box6", money.Cents(box("6")), d(9990))
	check(t, "box7", money.Cents(box("7")), d(4950))
	check(t, "box8", money.Cents(box("8")), d(5040))
	check(t, "box14 value", money.Cents(box("14")), d(10000))
	check(t, "box9", money.Cents(box("9")), d(20000))
	for _, x := range r.Boxes {
		if x.Box == "14" && (x.YesNo == nil || !*x.YesNo) {
			t.Error("SRRC line must set box 14 (reverse charge)")
		}
	}
	if _, err := ComputeGSTF5(GSTInput{PeriodStart: date(2026, 1, 1), PeriodEnd: date(2026, 3, 31), Lines: []GSTLine{{Kind: "sale", Code: "XX"}}}); err == nil {
		t.Error("unknown GST code must fail")
	}
}

func TestGSTReverseChargePurchase(t *testing.T) {
	// A reverse-charge purchase (TXRC): the imported services value goes to boxes 1, 5 and 14, the output tax at the
	// period rate to box 6, and the claimable input tax (the line's GST) to box 7.
	cases := []struct {
		name                                string
		gst                                 money.Cents
		box1, box5, box6, box7, box8, box14 money.Cents
	}{
		{"fully recoverable", d(900), d(10000), d(10000), d(900), d(900), 0, d(10000)},
		{"not recoverable", 0, d(10000), d(10000), d(900), 0, d(900), d(10000)},
		{"half recoverable", d(450), d(10000), d(10000), d(900), d(450), d(450), d(10000)},
		// The IRAS reverse-charge guide's worked example: S$10,000 of imported services at 9%
		// with a 60% residual recovery rate. Output tax S$900 to box 6, claimable S$540 to
		// box 7, net S$360 payable in box 8, S$10,000 in boxes 1, 5 and 14.
		{"iras guide 60% recovery", d(540), d(10000), d(10000), d(900), d(540), d(360), d(10000)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := ComputeGSTF5(GSTInput{PeriodStart: date(2026, 1, 1), PeriodEnd: date(2026, 3, 31), Lines: []GSTLine{
				{Kind: "purchase", Code: "TXRC", Net: d(10000), GST: c.gst},
			}})
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]GSTBox{}
			for _, x := range r.Boxes {
				got[x.Box] = x
			}
			check(t, "box1", got["1"].Value, c.box1)
			check(t, "box5", got["5"].Value, c.box5)
			check(t, "box6", got["6"].Value, c.box6)
			check(t, "box7", got["7"].Value, c.box7)
			check(t, "box8", got["8"].Value, c.box8)
			check(t, "box14", got["14"].Value, c.box14)
			if got["14"].YesNo == nil || !*got["14"].YesNo {
				t.Error("box 14 must be Yes for a reverse-charge purchase")
			}
			if len(r.Warnings) != 0 {
				t.Errorf("unexpected warnings: %v", r.Warnings)
			}
		})
	}
}

func TestGSTF5RateChangeAndMonthly(t *testing.T) {
	r, err := ComputeGSTF5(GSTInput{PeriodStart: date(2023, 12, 1), PeriodEnd: date(2024, 1, 31), Lines: []GSTLine{{Kind: "sale", Code: "SR", Net: d(100), GST: d(9)}}})
	if err != nil {
		t.Fatal(err)
	}
	if r.RateBP != 900 {
		t.Errorf("rate %d", r.RateBP)
	}
	var warned bool
	for _, w := range r.Warnings {
		if strings.Contains(w, "rate change") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("expected rate-change warning: %v", r.Warnings)
	}
	// Monthly period: July 2023 -> due 31 Aug 2023 (IRAS F5 guide example).
	r, _ = ComputeGSTF5(GSTInput{PeriodStart: date(2023, 7, 1), PeriodEnd: date(2023, 7, 31)})
	if !r.DueDate.Equal(date(2023, 8, 31)) {
		t.Errorf("monthly due %v", r.DueDate)
	}
	if r.RateBP != 800 {
		t.Errorf("2023 rate %d", r.RateBP)
	}
}

func TestACRA(t *testing.T) {
	r := computeACRA(ACRAInput{FYE: date(2025, 12, 31), IncorporationDate: date(2022, 3, 1), Current: SizeTest{Revenue: d(400000), TotalAssets: d(300000), Employees: 3},
		PriorYears: []SizeTest{{Revenue: d(350000), TotalAssets: d(250000), Employees: 3}, {Revenue: d(300000), TotalAssets: d(200000), Employees: 2}}})
	if !r.AuditExempt || r.FSFiling != "simplified_xbrl" || !r.SFRSSmallEntities {
		t.Errorf("unexpected %+v", r)
	}
	if !r.AGMDue.Equal(date(2026, 6, 30)) || !r.AnnualReturnDue.Equal(date(2026, 7, 31)) {
		t.Errorf("deadlines %v %v", r.AGMDue, r.AnnualReturnDue)
	}
	r = computeACRA(ACRAInput{FYE: date(2025, 3, 15), EPC: true, Solvent: true, Current: SizeTest{Revenue: d(20000000), TotalAssets: d(9000000), Employees: 80}})
	if r.FSFiling != "none" || !r.AGMDue.Equal(date(2025, 9, 15)) {
		t.Errorf("EPC %+v", r)
	}
}

func TestACRANotSmall(t *testing.T) {
	// Fails 2 of 3 in the preceding years ($10m revenue/assets, 50 staff): audit required, full XBRL.
	big := SizeTest{Revenue: d(20000000), TotalAssets: d(15000000), Employees: 80}
	r := computeACRA(ACRAInput{FYE: date(2025, 12, 31), Current: big, PriorYears: []SizeTest{big, big}})
	if r.SmallCompany || r.AuditExempt || r.SFRSSmallEntities {
		t.Errorf("large company must not be exempt: %+v", r)
	}
	if r.FSFiling != "full_xbrl" {
		t.Errorf("filing %s", r.FSFiling)
	}
	// Listed companies are never small; dormant unlisted companies are audit-exempt.
	r = computeACRA(ACRAInput{FYE: date(2025, 12, 31), Listed: true, Current: SizeTest{Revenue: d(100), TotalAssets: d(100), Employees: 1}})
	if r.SmallCompany || r.AuditExempt {
		t.Errorf("listed must not be exempt: %+v", r)
	}
	r = computeACRA(ACRAInput{FYE: date(2025, 12, 31), Dormant: true, DormantConfirmed: true, ListedSubsidiaryConfirmed: true, Current: SizeTest{Revenue: d(100), TotalAssets: d(100), Employees: 1}})
	if !r.AuditExempt || r.FSFiling == "none" {
		t.Errorf("dormant %+v", r)
	}
	r = computeACRA(ACRAInput{FYE: date(2025, 12, 31), Dormant: true, DormantConfirmed: true, DormantRelevant: true, DormantRelevantConfirmed: true, GroupStatusConfirmed: true, ListedSubsidiaryConfirmed: true, Current: SizeTest{Revenue: d(100), TotalAssets: d(100), Employees: 1}})
	if r.FSFiling != "none" {
		t.Errorf("confirmed dormant relevant company should not need to file financial statements: %+v", r)
	}
	// One bad year out of two still fails the two-consecutive-years test.
	r = computeACRA(ACRAInput{FYE: date(2025, 12, 31), Current: SizeTest{Revenue: d(100), TotalAssets: d(100), Employees: 1},
		PriorYears: []SizeTest{big, {Revenue: d(100), TotalAssets: d(100), Employees: 1}}})
	if r.SmallCompany {
		t.Errorf("mixed years must fail: %+v", r)
	}
}

// A group member that passes the size test alone is not audit-exempt until the small-group test on
// consolidated figures passes (Thirteenth Schedule para 1(b), 7 and 12).
func TestACRAGroupMember(t *testing.T) {
	small := SizeTest{Revenue: d(5000000), TotalAssets: d(5000000), Employees: 10}
	base := ACRAInput{FYE: date(2025, 12, 31), IncorporationDate: date(2022, 3, 1), Current: small, PriorYears: []SizeTest{small, small}, PartOfGroup: true}

	r := computeACRA(base)
	if !r.SmallCompany || r.AuditExempt {
		t.Fatalf("group member without group test must not be exempt: %+v", r)
	}
	base.GroupYears = []SizeTest{
		{Revenue: d(5000000), TotalAssets: d(5000000), Employees: 10, FinancialYearEnd: "2025-12-31"},
		{Revenue: d(5000000), TotalAssets: d(5000000), Employees: 10, FinancialYearEnd: "2024-12-31"},
		{Revenue: d(5000000), TotalAssets: d(5000000), Employees: 10, FinancialYearEnd: "2023-12-31"},
	}
	base.GroupFirstFinancialYearEnd = date(2023, 12, 31)
	base.GroupEvidenceReference = "consolidated group accounts FY2023-2025"
	r = computeACRA(base)
	if !r.SmallCompany || !r.AuditExempt {
		t.Errorf("group member with group test passed must be exempt: %+v", r)
	}
	// Size test failing is still not exempt, with or without the group test.
	big := SizeTest{Revenue: d(20000000), TotalAssets: d(15000000), Employees: 80}
	r = computeACRA(ACRAInput{FYE: date(2025, 12, 31), Current: big, PriorYears: []SizeTest{big, big}, PartOfGroup: true, GroupYears: []SizeTest{{Revenue: d(20000000), TotalAssets: d(15000000), Employees: 80, FinancialYearEnd: "2025-12-31"}, {Revenue: d(20000000), TotalAssets: d(15000000), Employees: 80, FinancialYearEnd: "2024-12-31"}, {Revenue: d(20000000), TotalAssets: d(15000000), Employees: 80, FinancialYearEnd: "2023-12-31"}}, GroupFirstFinancialYearEnd: date(2023, 12, 31), GroupEvidenceReference: "consolidated group accounts FY2023-2025"})
	if r.SmallCompany || r.AuditExempt {
		t.Errorf("large group member must not be exempt: %+v", r)
	}
}

// Second financial year, Thirteenth Schedule paras 3(a), 3(b), 6(a) and 13(a): the company is small if
// it meets 2 of 3 in year 1 (carried forward) or in year 2 on its own.
func TestACRASecondYear(t *testing.T) {
	y1Big := SizeTest{Revenue: d(12000000), TotalAssets: d(12000000), Employees: 60}
	y2Small := SizeTest{Revenue: d(5000000), TotalAssets: d(5000000), Employees: 10}
	y1Small := SizeTest{Revenue: d(5000000), TotalAssets: d(5000000), Employees: 10}
	y2Big := SizeTest{Revenue: d(12000000), TotalAssets: d(12000000), Employees: 60}

	// Audit probe: year 1 fails all three; year 2 passes all three on its own under para 3(b).
	r := computeACRA(ACRAInput{FYE: date(2025, 12, 31), IncorporationDate: date(2024, 3, 1), Current: y2Small, PriorYears: []SizeTest{y1Big}})
	if !r.SmallCompany || !r.AuditExempt {
		t.Errorf("second year qualifying on its own must be small and exempt: %+v", r)
	}
	// Year 1 qualifies under 3(a); it stays small in year 2 (13(a), 6(a)) even though year 2 fails.
	r = computeACRA(ACRAInput{FYE: date(2025, 12, 31), IncorporationDate: date(2024, 3, 1), Current: y2Big, PriorYears: []SizeTest{y1Small}})
	if !r.SmallCompany || !r.AuditExempt {
		t.Errorf("first year qualifying must carry into second year: %+v", r)
	}
	// Neither year qualifies.
	r = computeACRA(ACRAInput{FYE: date(2025, 12, 31), IncorporationDate: date(2024, 3, 1), Current: y2Big, PriorYears: []SizeTest{y1Big}})
	if r.SmallCompany || r.AuditExempt {
		t.Errorf("second year failing both years must not be small: %+v", r)
	}
	// Listed companies are never small, in any year.
	r = computeACRA(ACRAInput{FYE: date(2025, 12, 31), IncorporationDate: date(2024, 3, 1), Listed: true, Current: y2Small, PriorYears: []SizeTest{y1Small}})
	if r.SmallCompany || r.AuditExempt {
		t.Errorf("listed second-year company must not be small: %+v", r)
	}
	// Second-year group member still needs the group test.
	r = computeACRA(ACRAInput{FYE: date(2025, 12, 31), IncorporationDate: date(2024, 3, 1), Current: y2Small, PriorYears: []SizeTest{y1Big}, PartOfGroup: true})
	if !r.SmallCompany || r.AuditExempt {
		t.Errorf("second-year group member without group test must not be exempt: %+v", r)
	}
}

// Supplementary and conditional F5 boxes: 15–17 are separate figures that cannot exceed the
// standard-rated supplies in box 1, 10–12 add their claims to input tax, and an answer without an
// amount (or the reverse) is warned about rather than silently filed.
func TestGSTSupplementaryAndConditionalBoxes(t *testing.T) {
	base := func() GSTInput {
		return GSTInput{PeriodStart: date(2026, 1, 1), PeriodEnd: date(2026, 3, 31), Lines: []GSTLine{
			{Kind: "sale", Code: "SR", Net: d(100000), GST: d(9000)},
			{Kind: "purchase", Code: "TX", Net: d(40000), GST: d(3600)},
		}}
	}
	in := base()
	in.MarketplaceRemote, in.MarketplaceRemoteAmount = true, d(20000)
	in.RedelivererLVG, in.RedelivererLVGAmount = true, d(5000)
	in.SuppliedLVG, in.SuppliedLVGAmount = true, d(3000)
	in.TouristRefundClaim, in.TouristRefundAmount = true, d(500)
	in.PreRegistrationPurchaseValue, in.PreRegistrationClaimAmount = d(1000), d(90)
	r, err := ComputeGSTF5(in)
	if err != nil {
		t.Fatal(err)
	}
	box := func(b string) money.Cents {
		for _, x := range r.Boxes {
			if x.Box == b {
				return x.Value
			}
		}
		t.Fatalf("box %s missing", b)
		return 0
	}
	if box("15") != d(20000) || box("16") != d(5000) || box("17") != d(3000) {
		t.Fatalf("supplementary boxes = %d/%d/%d", box("15"), box("16"), box("17"))
	}
	if box("5") != d(41000) {
		t.Fatalf("box 5 = %d, want the taxable purchases plus the pre-registration purchase value", box("5"))
	}
	if box("7") != d(3600+500+90) {
		t.Fatalf("box 7 = %d, want input tax plus the box 10 and 12 claims", box("7"))
	}

	// The three LVG boxes together cannot exceed box 1.
	over := base()
	over.MarketplaceRemote, over.MarketplaceRemoteAmount = true, d(90000)
	over.SuppliedLVG, over.SuppliedLVGAmount = true, d(20000)
	if _, err := ComputeGSTF5(over); err == nil || !strings.Contains(err.Error(), "cannot exceed") {
		t.Fatalf("boxes 15-17 above box 1 = %v, want an error", err)
	}
	// An answer with no amount is flagged for review, not filed silently.
	gap := base()
	gap.MarketplaceRemote = true
	r, err = ComputeGSTF5(gap)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range r.Warnings {
		if strings.Contains(w, "Box 15 answer and amount do not agree") {
			found = true
		}
	}
	if !found {
		t.Fatalf("box 15 answer without an amount was not warned: %v", r.Warnings)
	}
}
