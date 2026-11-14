package taxrules

import (
	"strings"
	"testing"
	"time"
)

// Expected results follow the Thirteenth Schedule text (https://sso.agc.gov.sg/Act/CoA1967: paras 2, 3,
// 5, 6, 13), not the implementation. Helpers date() and d() are defined in other_test.go and cit_test.go.

func acraNoteContains(r ACRAResult, part string) bool {
	for _, n := range r.Notes {
		if strings.Contains(n, part) {
			return true
		}
	}
	return false
}

// computeACRA supplies facts the legacy fixtures intend to treat as established. New tests should
// call ComputeACRA directly when they exercise an unknown or incomplete fact.
func computeACRA(in ACRAInput) ACRAResult {
	if in.FirstFinancialYearEnd.IsZero() && !in.IncorporationDate.IsZero() && !in.FYE.IsZero() {
		firstFYE := in.FYE
		for prior := addMonthsEnd(firstFYE, -12); prior.After(in.IncorporationDate); prior = addMonthsEnd(prior, -12) {
			firstFYE = prior
		}
		if firstFYE.After(in.IncorporationDate) {
			in.FirstFinancialYearEnd = firstFYE
		}
	}
	in.PrivateCompany = true
	in.PrivateCompanyConfirmed = true
	in.PublicAccountabilityConfirmed = true
	in.GeneralPurposeStatementsConfirmed = true
	in.PublishesGeneralPurposeStatements = true
	in.DormantConfirmed = true
	in.DormantRelevantConfirmed = true
	in.GroupStatusConfirmed = true
	in.ListedSubsidiaryConfirmed = true
	if in.EPC {
		in.EPCConfirmed = true
		if in.ShareholderCount == nil {
			n := 10
			in.ShareholderCount = &n
		}
	}
	if in.Solvent {
		in.SolvencyConfirmed = true
	}
	for i := range in.PriorYears {
		if in.PriorYears[i].FinancialYearEnd == "" && !in.FYE.IsZero() {
			in.PriorYears[i].FinancialYearEnd = addMonthsEnd(in.FYE, -12*(i+1)).Format("2006-01-02")
		}
	}
	return ComputeACRA(in)
}

// Para 3(a): a company in its first financial year after incorporation is small if it meets any 2 of the
// 3 criteria for that year. "Does not exceed" and "not more than" are inclusive.
func TestACRAYearOneMeetsTwoOfThree(t *testing.T) {
	inc, fye := date(2026, 3, 1), date(2026, 12, 31)
	cases := []struct {
		name string
		cur  SizeTest
		want bool
	}{
		{"revenue and employees pass, assets fail", SizeTest{Revenue: d(9000000), TotalAssets: d(12000000), Employees: 40}, true},
		{"assets pass only", SizeTest{Revenue: d(12000000), TotalAssets: d(9000000), Employees: 60}, false},
		{"employees pass only", SizeTest{Revenue: d(12000000), TotalAssets: d(12000000), Employees: 10}, false},
		{"exactly $10m revenue and 50 employees pass", SizeTest{Revenue: d(10000000), TotalAssets: d(12000000), Employees: 50}, true},
		{"exactly $10m revenue but 51 employees", SizeTest{Revenue: d(10000000), TotalAssets: d(12000000), Employees: 51}, false},
	}
	for _, c := range cases {
		r := computeACRA(ACRAInput{FYE: fye, IncorporationDate: inc, Current: c.cur})
		if r.SmallCompany != c.want || r.AuditExempt != c.want {
			t.Errorf("%s: small=%v exempt=%v, want %v: %+v", c.name, r.SmallCompany, r.AuditExempt, c.want, r)
		}
	}
}

// Para 3(b) and paras 13(a), 6(a): the second financial year is small if the first year meets 2 of 3
// (carried forward, since para 5 does not apply before the third year) or the second year does on its own.
func TestACRAYearTwoUsesFirstYearOrSecondYear(t *testing.T) {
	inc, fye2 := date(2025, 3, 1), date(2026, 12, 31)
	y1Small := SizeTest{Revenue: d(9000000), TotalAssets: d(9000000), Employees: 80} // 2 of 3
	y1Big := SizeTest{Revenue: d(12000000), TotalAssets: d(12000000), Employees: 60} // 0 of 3
	y2Small := SizeTest{Revenue: d(5000000), TotalAssets: d(5000000), Employees: 10} // 3 of 3
	y2Big := SizeTest{Revenue: d(12000000), TotalAssets: d(12000000), Employees: 60} // 0 of 3
	cases := []struct {
		name  string
		prior SizeTest
		cur   SizeTest
		want  bool
	}{
		{"first year small carries into second year", y1Small, y2Big, true},
		{"second year small on its own", y1Big, y2Small, true},
		{"neither year small", y1Big, y2Big, false},
		{"both years small", y1Small, y2Small, true},
	}
	for _, c := range cases {
		withInc := computeACRA(ACRAInput{FYE: fye2, IncorporationDate: inc, Current: c.cur, PriorYears: []SizeTest{c.prior}})
		if withInc.SmallCompany != c.want || withInc.AuditExempt != c.want {
			t.Errorf("%s (incorporation date known): small=%v exempt=%v, want %v", c.name, withInc.SmallCompany, withInc.AuditExempt, c.want)
		}
		// Same position inferred from prior years when the incorporation date is unknown.
		fallback := computeACRA(ACRAInput{FYE: fye2, Current: c.cur, PriorYears: []SizeTest{c.prior}})
		if fallback.SmallCompany || fallback.AuditExempt {
			t.Errorf("%s (fallback with missing incorporation date): small=%v exempt=%v, want false", c.name, fallback.SmallCompany, fallback.AuditExempt)
		}
	}

	// First-year figures not supplied: only the second-year test can be applied, and the note says so.
	r := computeACRA(ACRAInput{FYE: fye2, IncorporationDate: inc, Current: y2Small})
	if !r.SmallCompany {
		t.Errorf("second year small on its own without first-year figures must be small: %+v", r)
	}
	r = computeACRA(ACRAInput{FYE: fye2, IncorporationDate: inc, Current: y2Big})
	if r.SmallCompany || r.AuditExempt || !acraNoteContains(r, "prior-year size figures are missing") {
		t.Errorf("second year without first-year figures must not be confirmed, with a note: %+v", r)
	}
}

// The financial year position is counted from the incorporation date, with year ends clamped to month end.
func TestACRAYearPositionFromIncorporation(t *testing.T) {
	cases := []struct {
		name string
		in   ACRAInput
		want int
	}{
		{"incorporated on a year end: that day is not the first year end", ACRAInput{IncorporationDate: date(2025, 12, 31), FYE: date(2026, 12, 31)}, 1},
		{"first year end", ACRAInput{IncorporationDate: date(2025, 3, 1), FYE: date(2025, 12, 31)}, 1},
		{"second year end", ACRAInput{IncorporationDate: date(2025, 3, 1), FYE: date(2026, 12, 31)}, 2},
		{"third year end", ACRAInput{IncorporationDate: date(2025, 3, 1), FYE: date(2027, 12, 31)}, 3},
		{"February year end clamps to month end", ACRAInput{IncorporationDate: date(2024, 3, 1), FYE: date(2026, 2, 28)}, 2},
		{"unknown incorporation date", ACRAInput{FYE: date(2026, 12, 31)}, 0},
		{"year end not after incorporation", ACRAInput{IncorporationDate: date(2025, 3, 1), FYE: date(2025, 3, 1)}, 0},
	}
	for _, c := range cases {
		if got := financialYearAfterIncorporation(c.in.IncorporationDate, c.in.FYE); got != c.want {
			t.Errorf("%s: position %d, want %d", c.name, got, c.want)
		}
	}
}

func TestACRAUsesRecordedFirstYearEndForExtendedFirstPeriod(t *testing.T) {
	input := ACRAInput{
		FYE: date(2025, 12, 31), IncorporationDate: date(2024, 6, 15),
		FirstFinancialYearEnd: date(2025, 12, 31),
		Current:               SizeTest{Revenue: d(5000000), TotalAssets: d(5000000), Employees: 10},
		PrivateCompany:        true, PrivateCompanyConfirmed: true, GroupStatusConfirmed: true,
	}
	if result := ComputeACRA(input); result.SmallCompanyStatus != ACRAEligible {
		t.Fatalf("a recorded 18-month first period should use the first-year size test: %+v", result)
	}

	input.FirstFinancialYearEnd = time.Time{}
	if result := ComputeACRA(input); result.SmallCompanyStatus != ACRANotEstablished {
		t.Fatalf("missing first-year end must not infer a positive result from incorporation date alone: %+v", result)
	}

	input.FirstFinancialYearEnd = date(2025, 6, 30)
	if result := ComputeACRA(input); result.SmallCompanyStatus != ACRANotEstablished {
		t.Fatalf("misaligned first-year end must not establish the current financial-year position: %+v", result)
	}
}

func TestACRASmallCompanyNeedsExplicitPrivateCompanyStatus(t *testing.T) {
	in := ACRAInput{
		FYE: date(2026, 12, 31), IncorporationDate: date(2026, 3, 1),
		FirstFinancialYearEnd: date(2026, 12, 31),
		Current:               SizeTest{Revenue: d(5000000), TotalAssets: d(5000000), Employees: 10},
		GroupStatusConfirmed:  true,
	}
	if result := ComputeACRA(in); result.SmallCompanyStatus != ACRANotEstablished {
		t.Fatalf("unconfirmed private-company status must remain unknown: %+v", result)
	}
	in.PrivateCompanyConfirmed = true
	if result := ComputeACRA(in); result.SmallCompanyStatus != ACRANotEligible {
		t.Fatalf("a confirmed public company must not qualify as a small company: %+v", result)
	}
	in.PrivateCompany = true
	if result := ComputeACRA(in); result.SmallCompanyStatus != ACRAEligible {
		t.Fatalf("a confirmed private company meeting the first-year test should qualify: %+v", result)
	}
}

// With no incorporation date, the year position comes from the prior years supplied, and a note says so.
func TestACRAUnknownIncorporationDateFallsBack(t *testing.T) {
	fye := date(2026, 12, 31)
	small := SizeTest{Revenue: d(5000000), TotalAssets: d(5000000), Employees: 10}
	big := SizeTest{Revenue: d(12000000), TotalAssets: d(12000000), Employees: 60}

	// No prior years: first year, 2 of 3 on this year.
	r := computeACRA(ACRAInput{FYE: fye, Current: small})
	if r.SmallCompany || r.AuditExempt || !acraNoteContains(r, "first financial year end") {
		t.Errorf("fallback first year: %+v", r)
	}
	// One prior year: second year, so a failing prior year does not stop a small current year.
	r = computeACRA(ACRAInput{FYE: fye, Current: small, PriorYears: []SizeTest{big}})
	if r.SmallCompany || r.AuditExempt {
		t.Errorf("fallback second year with missing incorporation date must not be confirmed: %+v", r)
	}
	r = computeACRA(ACRAInput{FYE: fye, Current: big, PriorYears: []SizeTest{big}})
	if r.SmallCompany || r.AuditExempt {
		t.Errorf("fallback second year failing both: %+v", r)
	}
	// Two prior years: third year onward, para 2 on both preceding years.
	r = computeACRA(ACRAInput{FYE: fye, Current: big, PriorYears: []SizeTest{small, small}})
	if r.SmallCompany || r.AuditExempt {
		t.Errorf("fallback third year with missing incorporation date must not be confirmed: %+v", r)
	}
	// An incorporation date after the year end is unusable and falls back to the same inference.
	r = computeACRA(ACRAInput{FYE: fye, IncorporationDate: date(2027, 1, 1), Current: small})
	if r.SmallCompany || r.SmallCompanyStatus != ACRANotEstablished || !acraNoteContains(r, "first financial year end") {
		t.Errorf("unusable incorporation date must leave status unestablished with a note: %+v", r)
	}
}

// Para 3(a) with the group test: a company in its first year that is part of a group is not audit-exempt
// until the group passes the small-group test on consolidated figures (Thirteenth Schedule para 1(b), 7-12).
func TestACRAYearOneGroupMemberNeedsGroupTest(t *testing.T) {
	small := SizeTest{Revenue: d(9000000), TotalAssets: d(9000000), Employees: 10}
	base := ACRAInput{FYE: date(2026, 12, 31), IncorporationDate: date(2026, 3, 1), FirstFinancialYearEnd: date(2026, 12, 31), Current: small, PartOfGroup: true}

	r := computeACRA(base)
	if !r.SmallCompany || r.AuditExempt || r.SmallGroupStatus != ACRANotEstablished || !acraNoteContains(r, "Group membership is confirmed") {
		t.Errorf("group member without group test must not be exempt: %+v", r)
	}
	base.GroupYears = []SizeTest{{Revenue: d(9000000), TotalAssets: d(9000000), Employees: 10, FinancialYearEnd: "2026-12-31"}}
	base.GroupFirstFinancialYearEnd = date(2026, 12, 31)
	base.GroupEvidenceReference = "consolidated group accounts FY2026"
	r = computeACRA(base)
	if !r.SmallCompany || !r.AuditExempt {
		t.Errorf("group member with group test passed must be exempt: %+v", r)
	}
	base.PartOfGroup = false
	base.GroupQualifies = false
	r = computeACRA(base)
	if !r.SmallCompany || !r.AuditExempt {
		t.Errorf("company outside a group needs no group test: %+v", r)
	}
}

// Para 2: from the third financial year, a company is small if both preceding years meet 2 of the 3 criteria.
func TestACRAThirdYearBothPrecedingYearsPass(t *testing.T) {
	small := SizeTest{Revenue: d(5000000), TotalAssets: d(5000000), Employees: 10}
	r := computeACRA(ACRAInput{FYE: date(2026, 12, 31), IncorporationDate: date(2024, 3, 1), Current: small, PriorYears: []SizeTest{small, small}})
	if !r.SmallCompany || !r.AuditExempt {
		t.Errorf("third year with both preceding years passing must be small: %+v", r)
	}
}

func TestACRASmallCompanyContinuationCessationAndUnknownHistory(t *testing.T) {
	small := SizeTest{Revenue: d(5000000), TotalAssets: d(5000000), Employees: 10, FinancialYearEnd: "2024-12-31"}
	big2025 := SizeTest{Revenue: d(12000000), TotalAssets: d(12000000), Employees: 60, FinancialYearEnd: "2025-12-31"}
	big2024 := big2025
	big2024.FinancialYearEnd = "2024-12-31"
	in := ACRAInput{FYE: date(2026, 12, 31), IncorporationDate: date(2022, 1, 1), FirstFinancialYearEnd: date(2022, 12, 31), Current: big2024,
		PriorYears: []SizeTest{big2025, small}, PrivateCompany: true, PrivateCompanyConfirmed: true, GroupStatusConfirmed: true}
	in.SmallCompanyHistory = QualificationHistory{Status: ACRAEligible, FinancialYearEnd: "2025-12-31", EvidenceReference: "reviewed FY2025 audit-exemption workpaper"}
	if result := ComputeACRA(in); result.SmallCompanyStatus != ACRAEligible || !result.AuditExempt {
		t.Fatalf("one failing and one passing year must continue a dated prior qualification: %+v", result)
	}
	in.PriorYears = []SizeTest{big2025, big2024}
	if result := ComputeACRA(in); result.SmallCompanyStatus != ACRANotEligible || result.AuditExempt {
		t.Fatalf("two consecutive failing years must end the prior qualification: %+v", result)
	}
	in.PriorYears = []SizeTest{big2025, small}
	in.SmallCompanyHistory = QualificationHistory{}
	if result := ComputeACRA(in); result.SmallCompanyStatus != ACRANotEstablished || result.AuditExemptionStatus != ACRANotEstablished {
		t.Fatalf("mixed years without prior qualification history must remain unknown: %+v", result)
	}
	small2025, small2024 := small, small
	small2025.FinancialYearEnd, small2024.FinancialYearEnd = "2025-12-31", "2024-12-31"
	in.PriorYears = []SizeTest{small2025, small2024}
	if result := ComputeACRA(in); result.SmallCompanyStatus != ACRAEligible {
		t.Fatalf("two qualifying years establish a new or renewed qualification without prior state: %+v", result)
	}
}

func TestACRASmallGroupContinuationAndCessationUseDatedGroupRecords(t *testing.T) {
	groupSmall := func(fye string) SizeTest {
		return SizeTest{Revenue: d(5000000), TotalAssets: d(5000000), Employees: 10, FinancialYearEnd: fye}
	}
	groupBig := func(fye string) SizeTest {
		return SizeTest{Revenue: d(12000000), TotalAssets: d(12000000), Employees: 60, FinancialYearEnd: fye}
	}
	company := groupSmall("2024-12-31")
	in := ACRAInput{
		FYE: date(2026, 12, 31), IncorporationDate: date(2022, 1, 1), FirstFinancialYearEnd: date(2022, 12, 31), Current: company,
		PriorYears:     []SizeTest{groupSmall("2025-12-31"), groupSmall("2024-12-31")},
		PrivateCompany: true, PrivateCompanyConfirmed: true, GroupStatusConfirmed: true, PartOfGroup: true,
		GroupFirstFinancialYearEnd: date(2023, 12, 31), GroupEvidenceReference: "consolidated group workpaper FY2024-2026",
		GroupYears: []SizeTest{groupSmall("2026-12-31"), groupSmall("2025-12-31"), groupSmall("2024-12-31")},
	}
	if result := ComputeACRA(in); result.SmallGroupStatus != ACRAEligible || result.AuditExemptionStatus != ACRAEligible {
		t.Fatalf("dated consolidated size history should establish the group test: %+v", result)
	}
	in.GroupYears = []SizeTest{groupSmall("2026-12-31"), groupBig("2025-12-31"), groupSmall("2024-12-31")}
	in.SmallGroupHistory = QualificationHistory{Status: ACRAEligible, FinancialYearEnd: "2025-12-31", EvidenceReference: "reviewed FY2025 group workpaper"}
	if result := ComputeACRA(in); result.SmallGroupStatus != ACRAEligible || result.AuditExemptionStatus != ACRAEligible {
		t.Fatalf("one failing group year must continue a dated prior small-group status: %+v", result)
	}
	in.GroupYears = []SizeTest{groupSmall("2026-12-31"), groupBig("2025-12-31"), groupBig("2024-12-31")}
	if result := ComputeACRA(in); result.SmallGroupStatus != ACRANotEligible || result.AuditExempt {
		t.Fatalf("two consecutive failing group years must end the prior group status: %+v", result)
	}
	in.GroupYears[1].FinancialYearEnd = "2023-12-31"
	if result := ComputeACRA(in); result.SmallGroupStatus != ACRANotEstablished || result.AuditExempt {
		t.Fatalf("misdated group history must not be used: %+v", result)
	}
}

func TestACRASFRSEligibilityDistinguishesEntryContinuationAndUnknown(t *testing.T) {
	small := SizeTest{Revenue: d(5000000), TotalAssets: d(5000000), Employees: 10}
	big := SizeTest{Revenue: d(12000000), TotalAssets: d(12000000), Employees: 60}
	in := ACRAInput{FYE: date(2026, 12, 31), IncorporationDate: date(2022, 1, 1), FirstFinancialYearEnd: date(2022, 12, 31), Current: big,
		PriorYears:                    []SizeTest{{Revenue: big.Revenue, TotalAssets: big.TotalAssets, Employees: big.Employees, FinancialYearEnd: "2025-12-31"}, {Revenue: small.Revenue, TotalAssets: small.TotalAssets, Employees: small.Employees, FinancialYearEnd: "2024-12-31"}},
		PublicAccountabilityConfirmed: true, GeneralPurposeStatementsConfirmed: true, PublishesGeneralPurposeStatements: true}
	in.SFRSSmallEntitiesHistory = QualificationHistory{Status: ACRAEligible, FinancialYearEnd: "2025-12-31", EvidenceReference: "reviewed FY2025 SFRS eligibility workpaper"}
	if result := ComputeACRA(in); result.SFRSSmallEntitiesStatus != ACRAEligible {
		t.Fatalf("one failed prior year must not immediately remove existing SFRS eligibility: %+v", result)
	}
	in.SFRSSmallEntitiesHistory = QualificationHistory{}
	if result := ComputeACRA(in); result.SFRSSmallEntitiesStatus != ACRANotEstablished {
		t.Fatalf("mixed history without framework status must remain unknown: %+v", result)
	}
	in.PriorYears[0] = SizeTest{Revenue: big.Revenue, TotalAssets: big.TotalAssets, Employees: big.Employees, FinancialYearEnd: "2025-12-31"}
	in.PriorYears[1] = SizeTest{Revenue: big.Revenue, TotalAssets: big.TotalAssets, Employees: big.Employees, FinancialYearEnd: "2024-12-31"}
	if result := ComputeACRA(in); result.SFRSSmallEntitiesStatus != ACRANotEligible {
		t.Fatalf("two failing consecutive years must disqualify SFRS for Small Entities: %+v", result)
	}
}

func TestACRAUnknownEvidenceDoesNotGrantExemptions(t *testing.T) {
	in := ACRAInput{FYE: date(2026, 12, 31), IncorporationDate: date(2024, 3, 1), Current: SizeTest{Revenue: d(100), TotalAssets: d(100), Employees: 1}, Dormant: true, EPC: true, Solvent: true}
	r := ComputeACRA(in)
	if r.AuditExempt || r.FSFiling == "none" || r.SFRSSmallEntities {
		t.Fatalf("unknown group, EPC, solvency, public-accountability and dormant-relevant facts must stay unestablished: %+v", r)
	}
}

func TestACRADormantAuditExemptionNeedsListedSubsidiaryAssessment(t *testing.T) {
	in := ACRAInput{FYE: date(2026, 12, 31), Dormant: true, DormantConfirmed: true}
	if r := ComputeACRA(in); r.AuditExempt {
		t.Fatal("dormant audit exemption must remain unknown until listed-subsidiary status is assessed")
	}
	in.ListedSubsidiaryConfirmed = true
	if r := ComputeACRA(in); !r.AuditExempt {
		t.Fatal("assessed dormant company outside a listed group should be audit exempt")
	}
	in.ListedSubsidiary = true
	if r := ComputeACRA(in); r.AuditExempt {
		t.Fatal("dormant subsidiary of a listed company should not receive the exemption")
	}
}

func TestACRASmallEntitiesUsesTwoDatedHistoricalYears(t *testing.T) {
	current := SizeTest{Revenue: d(9000000), TotalAssets: d(9000000), Employees: 20}
	y1, y2 := current, current
	y1.FinancialYearEnd = "2025-12-31"
	y2.FinancialYearEnd = "2024-12-31"
	in := ACRAInput{FYE: date(2026, 12, 31), IncorporationDate: date(2022, 1, 1), FirstFinancialYearEnd: date(2022, 12, 31), Current: current, PriorYears: []SizeTest{y1, y2}, PublicAccountabilityConfirmed: true, GeneralPurposeStatementsConfirmed: true, PublishesGeneralPurposeStatements: true}
	r := ComputeACRA(in)
	if !r.SFRSSmallEntities {
		t.Fatalf("two qualifying dated prior years should establish eligibility: %+v", r)
	}
	in.PriorYears[0].FinancialYearEnd = "2023-12-31"
	r = ComputeACRA(in)
	if r.SFRSSmallEntities {
		t.Fatalf("misdated historical evidence must not establish eligibility: %+v", r)
	}
}

func TestACRASmallEntitiesNewCompanyExceptionNeedsQualitativeFacts(t *testing.T) {
	current := SizeTest{Revenue: d(5000000), TotalAssets: d(5000000), Employees: -1}
	in := ACRAInput{
		FYE: date(2026, 12, 31), IncorporationDate: date(2026, 3, 1), FirstFinancialYearEnd: date(2026, 12, 31), Current: current,
		PublicAccountabilityConfirmed: true, GeneralPurposeStatementsConfirmed: true, PublishesGeneralPurposeStatements: true,
	}
	if result := ComputeACRA(in); !result.SFRSSmallEntities {
		t.Fatalf("new company should qualify in the first two years when qualitative conditions are confirmed: %+v", result)
	}
	in.Current.Revenue, in.Current.TotalAssets = d(12000000), d(12000000)
	if result := ComputeACRA(in); !result.SFRSSmallEntities {
		t.Fatalf("new-company exception should not require the normal historic size test in the first two years: %+v", result)
	}
}

func TestACRASmallEntitiesSecondYearNeedsBothDatedYears(t *testing.T) {
	first := SizeTest{Revenue: d(9000000), TotalAssets: d(9000000), Employees: 20, FinancialYearEnd: "2025-12-31"}
	current := SizeTest{Revenue: d(9000000), TotalAssets: d(9000000), Employees: 20}
	in := ACRAInput{
		FYE: date(2026, 12, 31), IncorporationDate: date(2025, 3, 1), FirstFinancialYearEnd: date(2025, 12, 31), Current: current, PriorYears: []SizeTest{first},
		PublicAccountabilityConfirmed: true, GeneralPurposeStatementsConfirmed: true, PublishesGeneralPurposeStatements: true,
	}
	if result := ComputeACRA(in); !result.SFRSSmallEntities {
		t.Fatalf("second-year company meeting size tests in both years should qualify: %+v", result)
	}
	in.PublishesGeneralPurposeStatements = false
	if result := ComputeACRA(in); result.SFRSSmallEntities || result.SFRSSmallEntitiesStatus != ACRANotEligible {
		t.Fatalf("missing general-purpose statements must make the entity ineligible: %+v", result)
	}
}
