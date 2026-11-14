package taxrules

import (
	"encoding/json"
	"strings"
	"time"

	"tidal/backend/internal/money"
)

// SizeTest is one financial year's size figures for the small-company tests.
type SizeTest struct {
	Revenue          money.Cents `json:"revenue"`
	TotalAssets      money.Cents `json:"totalAssets"`
	Employees        int         `json:"employees"`
	FinancialYearEnd string      `json:"financialYearEnd,omitempty"`
	invalid          bool
}

func (s *SizeTest) UnmarshalJSON(data []byte) error {
	var row struct {
		Revenue          *money.Cents `json:"revenue"`
		TotalAssets      *money.Cents `json:"totalAssets"`
		Employees        *int         `json:"employees"`
		FinancialYearEnd *string      `json:"financialYearEnd"`
	}
	if err := json.Unmarshal(data, &row); err != nil {
		return err
	}
	s.invalid = row.Revenue == nil || row.TotalAssets == nil || row.Employees == nil || row.FinancialYearEnd == nil || *row.FinancialYearEnd == ""
	if row.Revenue != nil {
		s.Revenue = *row.Revenue
	}
	if row.TotalAssets != nil {
		s.TotalAssets = *row.TotalAssets
	}
	if row.Employees != nil {
		s.Employees = *row.Employees
	}
	if row.FinancialYearEnd != nil {
		s.FinancialYearEnd = *row.FinancialYearEnd
	}
	return nil
}

func (s SizeTest) Invalid() bool { return s.invalid }

// QualificationHistory records an independently assessed status as at a dated year end.
// The evidence reference is required before a prior positive or negative status can carry
// into the current assessment.
type QualificationHistory struct {
	Status            string `json:"status"` // eligible, not_eligible, or not_established
	FinancialYearEnd  string `json:"financialYearEnd"`
	EvidenceReference string `json:"evidenceReference"`
}

const (
	ACRAEligible       = "eligible"
	ACRANotEligible    = "not_eligible"
	ACRANotEstablished = "not_established"
)

type ACRAInput struct {
	FYE                               time.Time            `json:"fye"`
	IncorporationDate                 time.Time            `json:"incorporationDate"` // zero = unknown; see ComputeACRA
	FirstFinancialYearEnd             time.Time            `json:"firstFinancialYearEnd"`
	Current                           SizeTest             `json:"current"`
	PriorYears                        []SizeTest           `json:"priorYears"` // most recent first; up to 2 used
	EPC                               bool                 `json:"epc"`        // exempt private company: <= 20 shareholders, no corporate shareholders
	EPCConfirmed                      bool                 `json:"epcConfirmed"`
	ShareholderCount                  *int                 `json:"shareholderCount,omitempty"`
	CorporateShareholder              bool                 `json:"corporateShareholder"`
	Solvent                           bool                 `json:"solvent"`
	SolvencyConfirmed                 bool                 `json:"solvencyConfirmed"`
	Listed                            bool                 `json:"listed"`
	PrivateCompany                    bool                 `json:"privateCompany"`
	PrivateCompanyConfirmed           bool                 `json:"privateCompanyConfirmed"`
	PubliclyAccount                   bool                 `json:"publiclyAccountable"`
	PublicAccountabilityConfirmed     bool                 `json:"publicAccountabilityConfirmed"`
	PublishesGeneralPurposeStatements bool                 `json:"publishesGeneralPurposeStatements"`
	GeneralPurposeStatementsConfirmed bool                 `json:"generalPurposeStatementsConfirmed"`
	Dormant                           bool                 `json:"dormant"`
	DormantConfirmed                  bool                 `json:"dormantConfirmed"`
	DormantRelevant                   bool                 `json:"dormantRelevant"`
	DormantRelevantConfirmed          bool                 `json:"dormantRelevantConfirmed"`
	ListedSubsidiary                  bool                 `json:"listedSubsidiary"`
	ListedSubsidiaryConfirmed         bool                 `json:"listedSubsidiaryConfirmed"`
	PartOfGroup                       bool                 `json:"partOfGroup"`
	GroupStatusConfirmed              bool                 `json:"groupStatusConfirmed"`
	GroupQualifies                    bool                 `json:"groupQualifies,omitempty"` // legacy input, ignored; retained for stored-return compatibility
	GroupYears                        []SizeTest           `json:"groupYears"`               // consolidated current year and up to 2 prior years, newest first
	GroupFirstFinancialYearEnd        time.Time            `json:"groupFirstFinancialYearEnd"`
	GroupEvidenceReference            string               `json:"groupEvidenceReference"`
	SmallCompanyHistory               QualificationHistory `json:"smallCompanyHistory"`
	SmallGroupHistory                 QualificationHistory `json:"smallGroupHistory"`
	SFRSSmallEntitiesHistory          QualificationHistory `json:"sfrsSmallEntitiesHistory"`
}

type ACRAResult struct {
	SmallCompany            bool      `json:"smallCompany"`
	SmallCompanyStatus      string    `json:"smallCompanyStatus"`
	SmallGroupStatus        string    `json:"smallGroupStatus"`
	AuditExempt             bool      `json:"auditExempt"`
	AuditExemptionStatus    string    `json:"auditExemptionStatus"`
	SFRSSmallEntities       bool      `json:"sfrsSmallEntities"`
	SFRSSmallEntitiesStatus string    `json:"sfrsSmallEntitiesStatus"`
	FSFiling                string    `json:"fsFiling"` // none, simplified_xbrl, full_xbrl
	FSFilingExplanation     string    `json:"fsFilingExplanation"`
	AGMDue                  time.Time `json:"agmDue"`
	AnnualReturnDue         time.Time `json:"annualReturnDue"`
	Notes                   []string  `json:"notes"`
}

const (
	smallRevenue   money.Cents = 1000000000 // $10m
	smallAssets    money.Cents = 1000000000 // $10m
	smallEmployees             = 50
	xbrlSmallLimit money.Cents = 50000000 // $500k revenue and total assets
)

func meetsTwoOfThree(s SizeTest) bool {
	return sizeStatus(s) == ACRAEligible
}

func sizeStatus(s SizeTest) string {
	passed, unknown := 0, 0
	if s.Revenue < 0 {
		unknown++
	} else if s.Revenue <= smallRevenue {
		passed++
	}
	if s.TotalAssets < 0 {
		unknown++
	} else if s.TotalAssets <= smallAssets {
		passed++
	}
	if s.Employees < 0 {
		unknown++
	} else if s.Employees <= smallEmployees {
		passed++
	}
	switch {
	case passed >= 2:
		return ACRAEligible
	case passed+unknown < 2:
		return ACRANotEligible
	default:
		return ACRANotEstablished
	}
}

func validHistory(h QualificationHistory, priorFYE time.Time) bool {
	return (h.Status == ACRAEligible || h.Status == ACRANotEligible) &&
		h.FinancialYearEnd == priorFYE.Format("2006-01-02") && strings.TrimSpace(h.EvidenceReference) != ""
}

// assessRollingQualification applies an entry test when both prior years qualify and otherwise
// carries a dated, evidenced prior qualification forward until both years fail. Unknown facts never
// become a negative qualification state.
func assessRollingQualification(years []SizeTest, fye time.Time, history QualificationHistory) string {
	if fye.IsZero() || len(years) < 2 {
		return ACRANotEstablished
	}
	statuses := make([]string, 2)
	for i := range statuses {
		want := addMonthsEnd(fye, -12*(i+1)).Format("2006-01-02")
		if years[i].invalid || years[i].FinancialYearEnd != want {
			return ACRANotEstablished
		}
		statuses[i] = sizeStatus(years[i])
	}
	if statuses[0] == ACRAEligible && statuses[1] == ACRAEligible {
		return ACRAEligible
	}
	if statuses[0] == ACRANotEligible && statuses[1] == ACRANotEligible {
		return ACRANotEligible
	}
	if (statuses[0] == ACRAEligible && statuses[1] == ACRANotEstablished) || (statuses[1] == ACRAEligible && statuses[0] == ACRANotEstablished) {
		if validHistory(history, addMonthsEnd(fye, -12)) && history.Status == ACRAEligible {
			return ACRAEligible // a known passing year prevents two consecutive failures
		}
		return ACRANotEstablished // the unknown year may complete a new entry test
	}
	if !validHistory(history, addMonthsEnd(fye, -12)) {
		return ACRANotEstablished
	}
	if history.Status == ACRANotEligible {
		return ACRANotEligible
	}
	// A prior qualification continues unless both immediately preceding years fail.
	if statuses[0] == ACRAEligible || statuses[1] == ACRAEligible {
		return ACRAEligible
	}
	return ACRANotEstablished
}

func combineOr(a, b string) string {
	if a == ACRAEligible || b == ACRAEligible {
		return ACRAEligible
	}
	if a == ACRANotEligible && b == ACRANotEligible {
		return ACRANotEligible
	}
	return ACRANotEstablished
}

func combineAnd(a, b string) string {
	if a == ACRANotEligible || b == ACRANotEligible {
		return ACRANotEligible
	}
	if a == ACRAEligible && b == ACRAEligible {
		return ACRAEligible
	}
	return ACRANotEstablished
}

func groupFinancialYearPosition(firstFYE, currentFYE time.Time) int {
	if firstFYE.IsZero() || currentFYE.IsZero() || currentFYE.Before(firstFYE) {
		return 0
	}
	for n := 1; n <= 100; n++ {
		if addMonthsEnd(firstFYE, 12*(n-1)).Equal(currentFYE) {
			return n
		}
	}
	return 0
}

// FinancialYearPosition returns the numbered year ending on currentFYE when counted from the
// recorded firstFYE, or zero when the two dates do not follow the annual year-end sequence.
func FinancialYearPosition(firstFYE, currentFYE time.Time) int {
	return groupFinancialYearPosition(firstFYE, currentFYE)
}

// PriorFinancialYearEnd returns the aligned prior year end, preserving month-end dates.
func PriorFinancialYearEnd(fye time.Time, years int) time.Time {
	if years < 1 {
		return fye
	}
	return addMonthsEnd(fye, -12*years)
}

func companyFinancialYearPosition(incorporationDate, firstFYE, currentFYE time.Time) int {
	if incorporationDate.IsZero() || firstFYE.IsZero() || !firstFYE.After(incorporationDate) {
		return 0
	}
	return FinancialYearPosition(firstFYE, currentFYE)
}

func assessSmallGroup(in ACRAInput) string {
	if !in.GroupStatusConfirmed {
		return ACRANotEstablished
	}
	if !in.PartOfGroup {
		return "not_applicable"
	}
	if strings.TrimSpace(in.GroupEvidenceReference) == "" || len(in.GroupYears) == 0 || in.GroupYears[0].invalid || in.GroupYears[0].FinancialYearEnd != in.FYE.Format("2006-01-02") {
		return ACRANotEstablished
	}
	current := sizeStatus(in.GroupYears[0])
	position := groupFinancialYearPosition(in.GroupFirstFinancialYearEnd, in.FYE)
	switch position {
	case 1:
		return current
	case 2:
		if len(in.GroupYears) < 2 || in.GroupYears[1].invalid || in.GroupYears[1].FinancialYearEnd != addMonthsEnd(in.FYE, -12).Format("2006-01-02") {
			return combineOr(current, ACRANotEstablished)
		}
		return combineOr(current, sizeStatus(in.GroupYears[1]))
	case 0:
		return ACRANotEstablished
	default:
		return assessRollingQualification(in.GroupYears[1:], in.FYE, in.SmallGroupHistory)
	}
}

// ComputeACRA returns separate, three-state assessments for small-company audit exemption,
// group qualification, and SFRS for Small Entities, as well as the ACRA XBRL filing category.
//
// Primary text: Companies Act 1967, s 205C(5) and Thirteenth Schedule "Criteria for small company and
// small group" (annotated [36/2014], inserted by the Companies (Amendment) Act 2014):
// https://sso.agc.gov.sg/Act/CoA1967 . Paragraph numbers below refer to that Schedule.
//
// Para 3 ("despite paragraph 2", where the company has not reached its third financial year after
// incorporation): a company is small (a) from its first financial year after incorporation if it is a
// private company throughout that year and satisfies any 2 of: revenue does not exceed $10 million; total
// assets at year end do not exceed $10 million; not more than 50 employees at year end; or (b) from its
// second financial year after incorporation, on the same test for that year.
//
// Paras 13(a) and 6(a): a company small from a financial year stays small in every later year until it
// ceases under para 5, and para 5 does not apply to a company that has not reached its third financial year
// after incorporation. So a company small under para 3(a) is still small in its second year.
//
// Para 2 (third financial year onward) is the entry test. Under para 5, an established qualification
// continues until the company fails the size test in each of the 2 immediately preceding years.
//
// Year position is counted from the recorded first financial year end. When the first year is longer than
// 12 months, inferring the position from incorporation date alone could select the wrong rule, so a missing
// or misaligned first year end leaves the classification unestablished.
func ComputeACRA(in ACRAInput) ACRAResult {
	r := ACRAResult{
		AGMDue:          addMonthsEnd(in.FYE, 6),
		AnnualReturnDue: addMonthsEnd(in.FYE, 7),
	}
	if in.Listed {
		r.AGMDue = addMonthsEnd(in.FYE, 4)
		r.AnnualReturnDue = addMonthsEnd(in.FYE, 5)
	}
	years := in.PriorYears
	if len(years) > 2 {
		years = years[:2]
	}
	n := companyFinancialYearPosition(in.IncorporationDate, in.FirstFinancialYearEnd, in.FYE)
	positionEstablished := n > 0
	if n == 0 {
		n = len(years) + 1
		r.Notes = append(r.Notes, "The incorporation date and the company's first financial year end are both required to establish whether this is its first, second or later financial year. The position inferred from prior-year rows is not used for a positive classification.")
	}
	if positionEstablished && n >= 2 {
		r.Notes = append(r.Notes, "The financial-year position is counted from the recorded first financial year end. Confirm that the historical financial years follow the same annual sequence.")
	}
	validPriorYears := func(count int) bool {
		if len(years) < count || in.FYE.IsZero() {
			return false
		}
		for i := 0; i < count; i++ {
			if years[i].invalid || years[i].FinancialYearEnd != addMonthsEnd(in.FYE, -12*(i+1)).Format("2006-01-02") {
				return false
			}
		}
		return true
	}
	companySizeStatus := sizeStatus(in.Current)
	privateStatus := ACRANotEstablished
	switch {
	case in.Listed:
		privateStatus = ACRANotEligible
	case in.PrivateCompanyConfirmed && in.PrivateCompany:
		privateStatus = ACRAEligible
	case in.PrivateCompanyConfirmed && !in.PrivateCompany:
		privateStatus = ACRANotEligible
	}
	switch n {
	case 1:
		// Para 3(a)(ii): 2 of the 3 criteria for the first financial year.
		r.SmallCompanyStatus = combineAnd(privateStatus, companySizeStatus)
		r.Notes = append(r.Notes, "First financial year after incorporation: small if this year meets 2 of the 3 criteria (Thirteenth Schedule para 3(a)).")
	case 2:
		// Para 3(b) for this year; para 3(a) for the first year, carried into this year by paras 13(a) and 6(a).
		if !validPriorYears(1) {
			r.SmallCompanyStatus = combineAnd(privateStatus, combineOr(companySizeStatus, ACRANotEstablished))
			r.Notes = append(r.Notes, "Second financial year after incorporation: the prior-year size figures are missing or are not tied to the immediately preceding financial year. This year's test can establish status; otherwise first-year figures are needed to confirm continuation.")
		} else {
			r.SmallCompanyStatus = combineAnd(privateStatus, combineOr(sizeStatus(years[0]), companySizeStatus))
			r.Notes = append(r.Notes, "Second financial year after incorporation: small if the first year meets 2 of the 3 criteria (para 3(a), carried forward by paras 13(a) and 6(a)) or this year does (para 3(b)).")
		}
	default:
		r.SmallCompanyStatus = combineAnd(privateStatus, assessRollingQualification(years, in.FYE, in.SmallCompanyHistory))
		r.Notes = append(r.Notes, "Third financial year onward: dated size records apply the para 2 entry test and the para 5 continuation/cessation test. A prior status is used only when its year end and evidence reference are recorded.")
	}
	if !positionEstablished && privateStatus != ACRANotEligible {
		r.SmallCompanyStatus = ACRANotEstablished
	}
	if in.Current.Employees < 0 {
		r.Notes = append(r.Notes, "The current financial year's full-time employee count is not recorded; any size conclusion that depends on that criterion is not established.")
	}
	r.SmallCompany = r.SmallCompanyStatus == ACRAEligible
	r.SmallGroupStatus = assessSmallGroup(in)
	if in.PartOfGroup && r.SmallGroupStatus == ACRANotEstablished {
		r.Notes = append(r.Notes, "Group membership is confirmed, but the dated consolidated size history and evidence reference do not establish small-group status.")
	}
	smallAuditStatus := r.SmallCompanyStatus
	if !in.GroupStatusConfirmed {
		smallAuditStatus = ACRANotEstablished
		r.Notes = append(r.Notes, "Group membership is not confirmed, so the audit-exemption assessment is not established.")
	} else if in.PartOfGroup {
		smallAuditStatus = combineAnd(smallAuditStatus, r.SmallGroupStatus)
	}
	dormantAuditStatus := ACRANotEstablished
	if in.DormantConfirmed {
		dormantAuditStatus = ACRANotEligible
		if in.Dormant {
			if in.ListedSubsidiaryConfirmed {
				if !in.Listed && !in.ListedSubsidiary {
					dormantAuditStatus = ACRAEligible
				}
			} else {
				r.Notes = append(r.Notes, "Listed-company subsidiary status is not confirmed, so the dormant-company audit exemption is not established.")
			}
		}
	} else if !r.SmallCompany {
		r.Notes = append(r.Notes, "Dormant status is not confirmed; if the company is dormant, the separate dormant-company audit exemption needs assessment.")
	}
	r.AuditExemptionStatus = combineOr(smallAuditStatus, dormantAuditStatus)
	r.AuditExempt = r.AuditExemptionStatus == ACRAEligible
	if in.EPC && in.EPCConfirmed && !in.epcFactsPass() {
		r.Notes = append(r.Notes, "EPC status is not supported by the recorded shareholder count and corporate-shareholder facts. Confirm beneficial ownership, the member limit and the applicable company definition.")
	}
	// ACRA sources conflict at exactly 20 members: the Companies Act EPC definition says "not more
	// than 20 members", while ACRA's current financial-statement guidance says "fewer than 20".
	// Do not claim the EPC filing exemption here; use the ordinary filing route until the conflict
	// is resolved with an accountant.
	epcTwenty := in.EPC && in.EPCConfirmed && in.epcFactsPass() && in.ShareholderCount != nil && *in.ShareholderCount == 20
	if epcTwenty {
		r.Notes = append(r.Notes, "Exactly 20 members is unresolved: the Companies Act 1967 EPC definition says 'not more than 20 members', while ACRA's current financial-statement guidance says 'fewer than 20'. The exempt private company financial-statement filing exemption is not applied at exactly 20 members pending resolution; confirm the filing treatment with an accountant.")
	}
	if !in.EPCConfirmed {
		r.Notes = append(r.Notes, "EPC status has not been confirmed. A solvent EPC filing exemption cannot be applied until the EPC conditions are established.")
	}
	if in.EPC && in.EPCConfirmed && in.epcFactsPass() && !in.SolvencyConfirmed {
		r.Notes = append(r.Notes, "Solvency has not been confirmed, so the solvent EPC financial statement filing exemption is not applied.")
	}

	if in.DormantConfirmed && in.Dormant && dormantAuditStatus == ACRAEligible {
		r.Notes = append(r.Notes, "A dormant company outside listed-company status and listed-company subsidiaries qualifies for the dormant audit exemption.")
	}
	sfrsStatus := ACRANotEstablished
	switch {
	case in.PublicAccountabilityConfirmed && in.PubliclyAccount:
		sfrsStatus = ACRANotEligible
	case !in.PublicAccountabilityConfirmed:
		sfrsStatus = ACRANotEstablished
	case in.GeneralPurposeStatementsConfirmed && !in.PublishesGeneralPurposeStatements:
		sfrsStatus = ACRANotEligible
	case !in.GeneralPurposeStatementsConfirmed:
		sfrsStatus = ACRANotEstablished
	case !positionEstablished:
		sfrsStatus = ACRANotEstablished
	case n <= 2:
		// Newly incorporated entities have a two-year SFRS for Small Entities exception.
		sfrsStatus = ACRAEligible
	default:
		sfrsStatus = assessRollingQualification(years, in.FYE, in.SFRSSmallEntitiesHistory)
	}
	r.SFRSSmallEntitiesStatus = sfrsStatus
	r.SFRSSmallEntities = sfrsStatus == ACRAEligible
	if !in.PublicAccountabilityConfirmed {
		r.Notes = append(r.Notes, "Public-accountability status is not confirmed, so eligibility for SFRS for Small Entities is not established.")
	}
	if !in.GeneralPurposeStatementsConfirmed {
		r.Notes = append(r.Notes, "Whether the company publishes general-purpose financial statements for external users is not confirmed, so SFRS for Small Entities eligibility is not established.")
	}
	if sfrsStatus == ACRANotEstablished && in.PublicAccountabilityConfirmed && !in.PubliclyAccount && in.GeneralPurposeStatementsConfirmed && in.PublishesGeneralPurposeStatements {
		r.Notes = append(r.Notes, "SFRS for Small Entities eligibility needs the applicable incorporation exception or two immediately preceding, date-matched small-entity assessments and, for continuation, a dated previous qualification with its evidence reference.")
	}

	switch {
	case in.EPC && in.EPCConfirmed && in.epcFactsPass() && !epcTwenty && in.Solvent && in.SolvencyConfirmed:
		r.FSFiling = "none"
		r.FSFilingExplanation = "Solvent exempt private company: not required to file financial statements with the annual return (voluntary filing allowed)."
	case in.EPC && in.EPCConfirmed && in.epcFactsPass() && !epcTwenty && !in.SolvencyConfirmed:
		r.FSFiling = "full_xbrl"
		r.FSFilingExplanation = "Solvency has not been confirmed. Use the applicable financial statement filing until the solvency declaration is established."
		r.Notes = append(r.Notes, "EPC status or solvency alone does not establish the financial statement filing exemption. Confirm the EPC conditions and the company's ability to pay debts as they fall due.")
	case in.DormantConfirmed && in.Dormant && in.DormantRelevantConfirmed && in.DormantRelevant &&
		in.GroupStatusConfirmed && !in.PartOfGroup && in.ListedSubsidiaryConfirmed && !in.ListedSubsidiary &&
		!in.Listed && in.Current.TotalAssets >= 0 && in.Current.TotalAssets <= xbrlSmallLimit:
		r.FSFiling = "none"
		r.FSFilingExplanation = "Dormant relevant company: exempt from preparing and filing financial statements if all statutory conditions are met; confirm the declaration in BizFile+."
	case in.DormantConfirmed && in.Dormant:
		r.Notes = append(r.Notes, "Dormancy alone does not establish the dormant relevant company exemption from preparing or filing financial statements. Confirm the statutory asset, dormancy, listing and group conditions; the current year-end assets must also be no more than $500,000.")
	case in.PublicAccountabilityConfirmed && !in.PubliclyAccount && in.Current.Revenue >= 0 && in.Current.Revenue <= xbrlSmallLimit && in.Current.TotalAssets >= 0 && in.Current.TotalAssets <= xbrlSmallLimit:
		r.FSFiling = "simplified_xbrl"
		r.FSFilingExplanation = "Revenue and total assets are each $500,000 or below: file the Simplified XBRL template (about 120 data elements) with a PDF of the directors-authorised financial statements."
	default:
		r.FSFiling = "full_xbrl"
		r.FSFilingExplanation = "File financial statements in the Full XBRL template (about 210 data elements) using the BizFinx Preparation Tool."
	}
	return r
}

func (in ACRAInput) epcFactsPass() bool {
	// A company must have at least one member, so zero and negative counts never qualify. The
	// Companies Act 1967 EPC definition says "not more than 20 members", while ACRA's current
	// financial-statement guidance says "fewer than 20"; ComputeACRA treats exactly 20 as an
	// unresolved boundary rather than a settled exemption.
	return in.ShareholderCount != nil && *in.ShareholderCount >= 1 && *in.ShareholderCount <= 20 && !in.CorporateShareholder
}

// financialYearAfterIncorporation returns the position of the financial year ending on fye, counted from
// incorporation: 1 is the first financial year after incorporation, 2 the second, and so on. The first year
// is assumed to end at the first year end after incorporation, and later years are 12 months each. A first
// year lengthened beyond 12 months (up to 18 months) is not recognised, so the position can be counted one year
// later than the legal position. This is conservative: no exemption is claimed that the law may not give. A zero
// IncorporationDate means the company is treated as not small. Returns 0 if either date is unknown or fye is not
// after incorporation.
func financialYearAfterIncorporation(incorporated, fye time.Time) int {
	if incorporated.IsZero() || fye.IsZero() || !fye.After(incorporated) {
		return 0
	}
	n := 1
	for k := 1; addMonthsEnd(fye, -12*k).After(incorporated); k++ {
		n++
	}
	return n
}

// addMonthsEnd adds months; a month-end date stays at the month end (31 Dec + 6 months = 30 Jun).
func addMonthsEnd(d time.Time, months int) time.Time {
	if d.IsZero() {
		return d
	}
	lastOfTarget := time.Date(d.Year(), d.Month()+time.Month(months)+1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	if d.AddDate(0, 0, 1).Day() == 1 || d.Day() > lastOfTarget.Day() {
		return lastOfTarget
	}
	return time.Date(d.Year(), d.Month()+time.Month(months), d.Day(), 0, 0, 0, 0, time.UTC)
}
