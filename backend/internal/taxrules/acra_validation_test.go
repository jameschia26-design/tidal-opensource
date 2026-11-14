package taxrules

import (
	"encoding/json"
	"strings"
	"testing"
)

// ACRA's legislative reform says an EPC has "not more than 20 members", but ACRA's current
// financial-statement guidance says "fewer than 20". The exactly-20 boundary is therefore
// unresolved: no EPC filing exemption is claimed, and the caller flags the conflict for an
// accountant while retaining the ordinary filing route.
func TestACRAEPCTwentyMemberBoundaryIsFlagged(t *testing.T) {
	base := ACRAInput{
		FYE: date(2026, 12, 31), IncorporationDate: date(2020, 1, 1), FirstFinancialYearEnd: date(2020, 12, 31),
		Current: SizeTest{Revenue: d(100), TotalAssets: d(100), Employees: 1},
		EPC:     true, EPCConfirmed: true, Solvent: true, SolvencyConfirmed: true, DormantConfirmed: true,
		PrivateCompany: true, PrivateCompanyConfirmed: true, GroupStatusConfirmed: true,
		PublicAccountabilityConfirmed: true, GeneralPurposeStatementsConfirmed: true, PublishesGeneralPurposeStatements: true,
	}
	nineteen := 19
	in := base
	in.ShareholderCount = &nineteen
	if r := ComputeACRA(in); acraNoteContains(r, "EPC status is not supported") || acraNoteContains(r, "Exactly 20 members is unresolved") {
		t.Errorf("19 shareholders: EPC wrongly rejected or boundary wrongly flagged: %v", r.Notes)
	}

	twenty := 20
	in = base
	in.ShareholderCount = &twenty
	r := ComputeACRA(in)
	if acraNoteContains(r, "EPC status is not supported") {
		t.Errorf("20 shareholders: the member count itself is within the legislative limit")
	}
	if !acraNoteContains(r, "Exactly 20 members is unresolved") || !acraNoteContains(r, "confirm the filing treatment with an accountant") {
		t.Errorf("20 shareholders: unresolved 20-member boundary must be flagged: %v", r.Notes)
	}
	if r.FSFiling == "none" {
		t.Errorf("20 shareholders: must not claim an EPC filing exemption at the unresolved boundary: %+v", r)
	}
	if !acraNoteContains(r, "filing exemption is not applied") {
		t.Errorf("20 shareholders: the note must say the exemption is not applied: %v", r.Notes)
	}
	in.SolvencyConfirmed = false
	if r := ComputeACRA(in); r.FSFiling != "simplified_xbrl" {
		t.Errorf("20 shareholders with unconfirmed solvency: ordinary small-company filing = %q, want simplified_xbrl", r.FSFiling)
	}

	n := 21
	in = base
	in.ShareholderCount = &n
	if r := ComputeACRA(in); !acraNoteContains(r, "EPC status is not supported") {
		t.Errorf("21 shareholders: EPC wrongly accepted")
	}
	if in.epcFactsPass() {
		t.Errorf("21 shareholders: epcFactsPass must be false")
	}

	// A corporate shareholder disqualifies the EPC at any member count.
	ten := 10
	in = base
	in.ShareholderCount = &ten
	in.CorporateShareholder = true
	if r := ComputeACRA(in); !acraNoteContains(r, "EPC status is not supported") {
		t.Errorf("corporate shareholder: EPC wrongly accepted")
	}
}

// A member count is only usable when it is a real company's member count: absent, zero and
// negative counts cannot qualify for the EPC treatment, while 1 to 20 are within the legislative
// limit. Exactly 20 still has its unresolved filing consequence handled separately.
func TestACRAEPCInvalidMemberCountsCannotQualify(t *testing.T) {
	base := ACRAInput{
		FYE: date(2026, 12, 31), IncorporationDate: date(2020, 1, 1), FirstFinancialYearEnd: date(2020, 12, 31),
		Current: SizeTest{Revenue: d(100), TotalAssets: d(100), Employees: 1},
		EPC:     true, EPCConfirmed: true, Solvent: true, SolvencyConfirmed: true, DormantConfirmed: true,
		PrivateCompany: true, PrivateCompanyConfirmed: true, GroupStatusConfirmed: true,
		PublicAccountabilityConfirmed: true, GeneralPurposeStatementsConfirmed: true, PublishesGeneralPurposeStatements: true,
	}
	if base.epcFactsPass() {
		t.Fatal("absent member count must not pass the EPC facts")
	}
	for _, n := range []int{-5, -1, 0} {
		in := base
		in.ShareholderCount = &n
		if in.epcFactsPass() {
			t.Errorf("%d members: must not qualify as an EPC", n)
		}
		r := ComputeACRA(in)
		if r.FSFiling == "none" {
			t.Errorf("%d members: must not claim an EPC filing exemption: %+v", n, r)
		}
		if !acraNoteContains(r, "EPC status is not supported") {
			t.Errorf("%d members: invalid member count must be flagged: %v", n, r.Notes)
		}
	}
	for _, n := range []int{1, 19, 20} {
		in := base
		in.ShareholderCount = &n
		if !in.epcFactsPass() {
			t.Errorf("%d members: count is within the legislative member limit", n)
		}
	}
	in := base
	ten := 10
	in.ShareholderCount = &ten
	in.CorporateShareholder = true
	if in.epcFactsPass() {
		t.Error("a corporate shareholder must disqualify the EPC at any member count")
	}
}

// An absent employee count (-1) must stay unknown; a real zero is a fact that can satisfy the
// employee criterion. They must not produce the same small-company conclusion.
func TestACRAAbsentEmployeeCountDiffersFromZero(t *testing.T) {
	base := ACRAInput{
		FYE: date(2026, 12, 31), IncorporationDate: date(2026, 3, 1), FirstFinancialYearEnd: date(2026, 12, 31),
		PrivateCompany: true, PrivateCompanyConfirmed: true, GroupStatusConfirmed: true,
		Current: SizeTest{Revenue: d(12000000), TotalAssets: d(5000000), Employees: -1},
	}
	if r := ComputeACRA(base); r.SmallCompanyStatus != ACRANotEstablished {
		t.Errorf("absent employee count: status %q, want not established: %+v", r.SmallCompanyStatus, r)
	}
	base.Current.Employees = 0
	if r := ComputeACRA(base); r.SmallCompanyStatus != ACRAEligible {
		t.Errorf("zero employees: status %q, want eligible (revenue fails, assets and employees pass): %+v", r.SmallCompanyStatus, r)
	}
}

// Decoding distinguishes an absent or null size fact from a real zero, and leaves the record
// invalid (never treated as zero) when a required field is missing.
func TestACRASizeTestJSONAbsentVersusZero(t *testing.T) {
	var zero SizeTest
	if err := json.Unmarshal([]byte(`{"revenue":0,"totalAssets":0,"employees":0,"financialYearEnd":"2025-12-31"}`), &zero); err != nil {
		t.Fatal(err)
	}
	if zero.Invalid() || zero.Revenue != 0 || zero.TotalAssets != 0 || zero.Employees != 0 {
		t.Fatalf("a real zero row must decode as valid zeroes: %+v", zero)
	}
	if got := sizeStatus(zero); got != ACRAEligible {
		t.Fatalf("zero figures must pass the size test, got %q", got)
	}
	for _, raw := range []string{
		`{"totalAssets":0,"employees":0,"financialYearEnd":"2025-12-31"}`,
		`{"revenue":null,"totalAssets":0,"employees":0,"financialYearEnd":"2025-12-31"}`,
		`{"revenue":0,"totalAssets":0,"employees":0}`,
		`{"revenue":0,"totalAssets":0,"employees":0,"financialYearEnd":""}`,
	} {
		var got SizeTest
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if !got.Invalid() {
			t.Errorf("%s: missing fact must decode as invalid, got %+v", raw, got)
		}
	}
}

// A negative figure is an unknown fact, not a passing zero; it must not help a positive
// conclusion.
func TestACRANegativeSizeFiguresStayUnknown(t *testing.T) {
	unknown := SizeTest{Revenue: -1, TotalAssets: -1, Employees: -1}
	if got := sizeStatus(unknown); got != ACRANotEstablished {
		t.Fatalf("all-unknown size status = %q, want not established", got)
	}
	if got := sizeStatus(SizeTest{Revenue: -1, TotalAssets: d(0), Employees: 0}); got != ACRAEligible {
		t.Fatalf("two known passing criteria with one unknown must pass, got %q", got)
	}
}

// An absent revenue or asset figure is unknown (negative), not a small figure. Unknown figures
// must not select the Simplified XBRL template.
func TestACRAUnknownFiguresDoNotSelectSimplifiedXBRL(t *testing.T) {
	in := ACRAInput{
		FYE:                           date(2026, 12, 31),
		Current:                       SizeTest{Revenue: -1, TotalAssets: d(100), Employees: 1},
		PublicAccountabilityConfirmed: true, PubliclyAccount: false,
	}
	if r := ComputeACRA(in); r.FSFiling != "full_xbrl" {
		t.Errorf("absent revenue: fsFiling = %q, want full_xbrl: %+v", r.FSFiling, r)
	}
	in.Current = SizeTest{Revenue: d(100), TotalAssets: -1, Employees: 1}
	if r := ComputeACRA(in); r.FSFiling != "full_xbrl" {
		t.Errorf("absent total assets: fsFiling = %q, want full_xbrl: %+v", r.FSFiling, r)
	}
	in.Current = SizeTest{Revenue: d(100), TotalAssets: d(100), Employees: 1}
	if r := ComputeACRA(in); r.FSFiling != "simplified_xbrl" {
		t.Errorf("known small figures: fsFiling = %q, want simplified_xbrl", r.FSFiling)
	}
}

// The filing category is a dependent output of the confirmed facts: a solvent EPC files nothing,
// while an unconfirmed solvency falls back to Full XBRL rather than claiming the exemption.
func TestACRAFSFilingFollowsConfirmations(t *testing.T) {
	n := 10
	base := ACRAInput{
		FYE: date(2026, 12, 31), IncorporationDate: date(2020, 1, 1), FirstFinancialYearEnd: date(2020, 12, 31),
		Current: SizeTest{Revenue: d(100), TotalAssets: d(100), Employees: 1},
		EPC:     true, EPCConfirmed: true, Solvent: true, DormantConfirmed: true,
		PrivateCompany: true, PrivateCompanyConfirmed: true, GroupStatusConfirmed: true,
		PublicAccountabilityConfirmed: true, GeneralPurposeStatementsConfirmed: true, PublishesGeneralPurposeStatements: true,
		ShareholderCount: &n,
	}
	if r := ComputeACRA(base); r.FSFiling != "full_xbrl" || !strings.Contains(r.FSFilingExplanation, "Solvency has not been confirmed") {
		t.Fatalf("unconfirmed solvency must not claim the EPC filing exemption: %+v", r)
	}
	base.SolvencyConfirmed = true
	if r := ComputeACRA(base); r.FSFiling != "none" {
		t.Fatalf("confirmed solvent EPC should file nothing, got %q", r.FSFiling)
	}
	base.Solvent = false
	if r := ComputeACRA(base); r.FSFiling == "none" {
		t.Fatalf("a non-solvent EPC must not use the filing exemption: %+v", r)
	}
}
