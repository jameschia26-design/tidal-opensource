package taxrules

import (
	"fmt"

	"tidal/backend/internal/money"
)

// Item is a labelled amount inside a tax computation bucket.
type Item struct {
	Label  string      `json:"label"`
	Amount money.Cents `json:"amount"`
	Reason string      `json:"reason,omitempty"`
	Ref    string      `json:"ref,omitempty"`
}

// AttributeVintage tracks one source YA of company tax attributes brought forward.
type AttributeVintage struct {
	Kind                string      `json:"kind"`
	SourceYA            int         `json:"sourceYa"`
	Amount              money.Cents `json:"amount"`
	ShareholdingStatus  string      `json:"shareholdingStatus"`
	SameBusinessStatus  string      `json:"sameBusinessStatus,omitempty"`
	EligibilityEvidence string      `json:"eligibilityEvidence,omitempty"`
	EvidenceReference   string      `json:"evidenceReference"`
}

func sum(items []Item) money.Cents {
	var t money.Cents
	for _, i := range items {
		t += i.Amount
	}
	return t
}

// CITInput mirrors the IRAS tax computation for a trading company (Form C-S Part B).
// All amounts are positive cents unless stated; they are rounded to whole dollars before computing.
type CITInput struct {
	YA      int         `json:"ya"`
	Revenue money.Cents `json:"revenue"`
	// NetProfitBeforeTax per the financial statements; negative for a loss. (Form C-S line 1)
	NetProfitBeforeTax money.Cents `json:"netProfitBeforeTax"`
	// SeparateSourceIncome is income in the P&L that is taxed separately (interest, gross rent, ...). (line 2)
	SeparateSourceIncome []Item      `json:"separateSourceIncome"`
	NonTaxableIncome     []Item      `json:"nonTaxableIncome"`  // line 3
	NonDeductible        []Item      `json:"nonDeductible"`     // line 4
	RRDeduction          money.Cents `json:"rrDeduction"`       // line 6, Section 14N
	EISDeductions        money.Cents `json:"eisDeductions"`     // line 7, enhanced deductions under EIS
	FurtherDeductions    money.Cents `json:"furtherDeductions"` // line 8

	BalancingCharge    money.Cents `json:"balancingCharge"`    // line 10
	UnutilisedCABF     money.Cents `json:"unutilisedCaBf"`     // line 11
	CurrentYearCA      money.Cents `json:"currentYearCa"`      // line 12
	BalancingAllowance money.Cents `json:"balancingAllowance"` // part of line 12
	UnutilisedLossesBF money.Cents `json:"unutilisedLossesBf"` // line 13

	NetRentalIncome    money.Cents `json:"netRentalIncome"`    // line 14c (a deficit is disregarded)
	InterestIncome     money.Cents `json:"interestIncome"`     // line 15
	OtherTaxableIncome money.Cents `json:"otherTaxableIncome"` // line 16

	UnutilisedDonationsBF money.Cents `json:"unutilisedDonationsBf"` // line 18
	// IPCDonations is the actual amount donated to approved IPCs; the multiplier is applied here. (line 19a)
	IPCDonations money.Cents `json:"ipcDonations"`
	// OverseasDonations are qualifying PTIS/OHAS donations at 100%. (line 19b)
	OverseasDonations money.Cents `json:"overseasDonations"`

	StartUpExemption bool `json:"startUpExemption"`
	LocalEmployee    bool `json:"localEmployee"`

	// Form C-S qualifying declarations (Part A).
	OnlyIncomeAt17            bool `json:"onlyIncomeAt17"`
	ClaimsCarryBack           bool `json:"claimsCarryBack"`
	ClaimsGroupRelief         bool `json:"claimsGroupRelief"`
	ClaimsInvestmentAllowance bool `json:"claimsInvestmentAllowance"`
	ClaimsForeignTaxCredit    bool `json:"claimsForeignTaxCredit"`
}

// FormLine is one numbered line of Form C-S / Form C-S (Lite).
type FormLine struct {
	CS    string      `json:"cs"`             // Form C-S line number
	Lite  string      `json:"lite,omitempty"` // Form C-S (Lite) line number, if the field appears there
	Label string      `json:"label"`
	Value money.Cents `json:"value"`
}

type CITResult struct {
	YA          int      `json:"ya"`
	FormType    string   `json:"formType"` // C-S Lite, C-S or C
	FormReasons []string `json:"formReasons"`
	// FormCSchedules lists Form C schedules not calculated for the selected case.
	FormCSchedules []string `json:"formCSchedules,omitempty"`

	SeparateSourceInP_L    money.Cents `json:"separateSourceInPl"`
	NonTaxableTotal        money.Cents `json:"nonTaxableTotal"`
	NonDeductibleTotal     money.Cents `json:"nonDeductibleTotal"`
	AdjustedBeforeOtherDed money.Cents `json:"adjustedBeforeOtherDeductions"` // line 5
	AdjustedBeforeCA       money.Cents `json:"adjustedBeforeCa"`              // line 9
	CANet                  money.Cents `json:"caNet"`                         // CA b/f + current + BA - BC
	AdjustedAfterCA        money.Cents `json:"adjustedAfterCa"`
	AdjustedAfterLosses    money.Cents `json:"adjustedAfterLosses"`
	SeparateSourceTaxable  money.Cents `json:"separateSourceTaxable"`
	TotalBeforeDonations   money.Cents `json:"totalBeforeDonations"` // line 17
	DonationsClaimed       money.Cents `json:"donationsClaimed"`
	TotalAfterDonations    money.Cents `json:"totalAfterDonations"` // line 20
	ChargeableIncome       money.Cents `json:"chargeableIncome"`    // before exempt amount
	ExemptAmount           money.Cents `json:"exemptAmount"`
	ExemptionScheme        string      `json:"exemptionScheme"`
	ChargeableAfterExempt  money.Cents `json:"chargeableAfterExempt"`
	GrossTax               money.Cents `json:"grossTax"`
	RebateBeforeGrant      money.Cents `json:"rebateBeforeGrant"`
	CashGrant              money.Cents `json:"cashGrant"`
	NetRebate              money.Cents `json:"netRebate"`
	NetTaxPayable          money.Cents `json:"netTaxPayable"`
	UnutilisedCACF         money.Cents `json:"unutilisedCaCf"`        // line 21
	UnutilisedLossesCF     money.Cents `json:"unutilisedLossesCf"`    // line 22
	UnutilisedDonationsCF  money.Cents `json:"unutilisedDonationsCf"` // line 23
	UsedCABF               money.Cents `json:"usedCaBf"`
	UsedLossesBF           money.Cents `json:"usedLossesBf"`
	UsedDonationsBF        money.Cents `json:"usedDonationsBf"`
	ECIWaived              bool        `json:"eciWaived"`
	Lines                  []FormLine  `json:"lines"`
	Warnings               []string    `json:"warnings"`
	Source                 string      `json:"source"`
}

func rd(c money.Cents) money.Cents { return c.RoundDollar() }

func roundItems(items []Item) []Item {
	out := make([]Item, len(items))
	for i, it := range items {
		it.Amount = rd(it.Amount)
		out[i] = it
	}
	return out
}

// ComputeCIT runs the IRAS trading-company tax computation.
func ComputeCIT(in CITInput) (CITResult, error) {
	y, err := CITFor(in.YA)
	if err != nil {
		return CITResult{}, err
	}
	r := CITResult{YA: in.YA, Source: y.Source}
	for _, v := range []money.Cents{in.RRDeduction, in.EISDeductions, in.FurtherDeductions, in.BalancingCharge, in.UnutilisedCABF, in.CurrentYearCA, in.BalancingAllowance, in.UnutilisedLossesBF, in.InterestIncome, in.OtherTaxableIncome, in.UnutilisedDonationsBF, in.IPCDonations, in.OverseasDonations, in.Revenue} {
		if v < 0 {
			return CITResult{}, fmt.Errorf("deduction, allowance, income and donation amounts must not be negative")
		}
	}
	ssi, nti, nde := roundItems(in.SeparateSourceIncome), roundItems(in.NonTaxableIncome), roundItems(in.NonDeductible)
	np := rd(in.NetProfitBeforeTax)

	r.SeparateSourceInP_L = sum(ssi)
	r.NonTaxableTotal = sum(nti)
	r.NonDeductibleTotal = sum(nde)
	r.AdjustedBeforeOtherDed = np - r.SeparateSourceInP_L - r.NonTaxableTotal + r.NonDeductibleTotal
	r.AdjustedBeforeCA = r.AdjustedBeforeOtherDed - rd(in.RRDeduction) - rd(in.EISDeductions) - rd(in.FurtherDeductions)

	bc, ucabf, cyca, ba, ulbf := rd(in.BalancingCharge), rd(in.UnutilisedCABF), rd(in.CurrentYearCA), rd(in.BalancingAllowance), rd(in.UnutilisedLossesBF)
	r.CANet = ucabf + cyca + ba - bc
	r.AdjustedAfterCA = r.AdjustedBeforeCA - r.CANet
	r.AdjustedAfterLosses = r.AdjustedAfterCA - ulbf

	netRent := money.Max(rd(in.NetRentalIncome), 0)
	if in.NetRentalIncome < 0 {
		r.Warnings = append(r.Warnings, "Net rental deficit is disregarded (IRAS Basic Tax Calculator note on net rental income).")
	}
	r.SeparateSourceTaxable = rd(in.InterestIncome) + netRent + rd(in.OtherTaxableIncome)
	r.TotalBeforeDonations = r.AdjustedAfterLosses + r.SeparateSourceTaxable

	// Capital allowances are set off first, then trade losses (current-year loss plus losses b/f),
	// against adjusted profit and separate-source income, as in the IRAS calculator.
	avail := money.Max(r.AdjustedBeforeCA, 0) + r.SeparateSourceTaxable
	caPool := r.CANet
	if caPool < 0 { // balancing charge exceeds allowances: it is taxable income
		avail += -caPool
		caPool = 0
	}
	caUsed := money.Min(caPool, avail)
	avail -= caUsed
	r.UnutilisedCACF = caPool - caUsed
	r.UsedCABF = money.Min(ucabf, caUsed)
	lossPool := ulbf + money.Max(-r.AdjustedBeforeCA, 0)
	lossUsed := money.Min(lossPool, avail)
	avail -= lossUsed
	r.UnutilisedLossesCF = lossPool - lossUsed
	r.UsedLossesBF = money.Min(ulbf, lossUsed)

	// Donations: brought-forward first, then qualifying overseas donations, then current-year IPC donations.
	udbf := rd(in.UnutilisedDonationsBF)
	ipc := rd(in.IPCDonations.MulRate(y.IPCDonationBP))
	overseas := rd(in.OverseasDonations)
	useBF := money.Min(udbf, avail)
	avail -= useBF
	r.UsedDonationsBF = useBF
	useOverseas := money.Min(overseas, avail)
	avail -= useOverseas
	useIPC := money.Min(ipc, avail)
	avail -= useIPC
	r.DonationsClaimed = useBF + useOverseas + useIPC
	r.UnutilisedDonationsCF = (udbf - useBF) + (ipc - useIPC)
	if overseas > useOverseas {
		r.Warnings = append(r.Warnings, "Unutilised qualifying overseas donations cannot be carried forward.")
	}
	r.TotalAfterDonations = r.TotalBeforeDonations - udbf - overseas - ipc
	r.ChargeableIncome = avail

	if in.StartUpExemption {
		r.ExemptionScheme = "Tax exemption scheme for new start-up companies"
		r.ExemptAmount = rd(Tiered(r.ChargeableIncome, y.StartUpExempt))
	} else {
		r.ExemptionScheme = "Partial tax exemption"
		r.ExemptAmount = rd(Tiered(r.ChargeableIncome, y.PartialExempt))
	}
	r.ChargeableAfterExempt = r.ChargeableIncome - r.ExemptAmount
	r.GrossTax = r.ChargeableAfterExempt.MulRate(y.RateBP)
	if y.RebateBP > 0 {
		r.RebateBeforeGrant = money.Min(r.GrossTax.MulRate(y.RebateBP), y.RebateCap)
		if in.LocalEmployee {
			r.CashGrant = y.CashGrant
		}
		r.NetRebate = money.Max(r.RebateBeforeGrant-r.CashGrant, 0)
	}
	r.NetTaxPayable = r.GrossTax - r.NetRebate
	r.ECIWaived = in.Revenue <= ECIWaiverRevenueLimit && r.ChargeableIncome == 0

	r.FormType, r.FormReasons = chooseForm(in)
	if r.FormType == "C" {
		r.FormCSchedules = missingFormCSchedules(in)
	}
	r.Warnings = append(r.Warnings, y.Notes...)
	if r.CashGrant > 0 {
		r.Warnings = append(r.Warnings, "Cash grant is paid separately by IRAS and only appears in the final Notice of Assessment.")
	}
	r.Lines = []FormLine{
		{"1", "1", "Net Profit/ Loss before Tax as per Financial Statements", np},
		{"2", "", "Separate Source Income", r.SeparateSourceInP_L},
		{"3", "18", "Non-Taxable Income", r.NonTaxableTotal},
		{"4", "", "Non-Tax Deductible Expenses", r.NonDeductibleTotal},
		{"5", "", "Adjusted Profit/ Loss before Other Deductions", r.AdjustedBeforeOtherDed},
		{"6", "19", "Deduction for Renovation or Refurbishment Works under Section 14N", rd(in.RRDeduction)},
		{"7", "", "Enhanced Deductions under Enterprise Innovation Scheme (EIS)", rd(in.EISDeductions)},
		{"8", "", "Further Deductions/ Other Deductions", rd(in.FurtherDeductions)},
		{"9", "2", "Adjusted Profit/ Loss before Capital Allowances", r.AdjustedBeforeCA},
		{"10", "3", "Balancing Charge", bc},
		{"11", "4", "Unutilised Capital Allowances brought forward", ucabf},
		{"12", "5", "Current Year Capital Allowances (incl. Balancing Allowance)", cyca + ba},
		{"13", "6", "Unutilised Losses brought forward", ulbf},
		{"14c", "7", "Net Rental Income", netRent},
		{"15", "8", "Interest Income", rd(in.InterestIncome)},
		{"16", "9", "Other Taxable Income", rd(in.OtherTaxableIncome)},
		{"17", "10", "Total Income/ Losses (before Donations)", r.TotalBeforeDonations},
		{"18", "11", "Unutilised Donations brought forward", udbf},
		{"19a", "12a", "Donations to Approved IPCs (at the qualifying multiplier)", ipc},
		{"19b", "12b", "Qualifying Overseas Donations (PTIS/ OHAS)", overseas},
		{"20", "13", "Total Income/ Losses (after Donations)", r.TotalAfterDonations},
		{"21", "14", "Unutilised Capital Allowances carried forward", r.UnutilisedCACF},
		{"22", "15", "Unutilised Losses carried forward", r.UnutilisedLossesCF},
		{"23", "16", "Unutilised Donations carried forward", r.UnutilisedDonationsCF},
	}
	return r, nil
}

func missingFormCSchedules(in CITInput) []string {
	var schedules []string
	if in.Revenue > FormCSRevenueLimit {
		schedules = append(schedules, "Form C main return and Form C-format tax computation; only Form C-S reference lines are used")
	}
	if !in.OnlyIncomeAt17 {
		schedules = append(schedules, "Income taxed at concessionary rates or exempt under a tax incentive, including income allocation and rate-specific computation")
	}
	if in.ClaimsCarryBack {
		schedules = append(schedules, "Carry-back of current-year capital allowances or trade losses, including the revised tax computation for the preceding YA")
	}
	if in.ClaimsGroupRelief {
		schedules = append(schedules, "Group Relief transferor / claimant schedules (Form GR-A and Form GR-B) and eligibility calculation")
	}
	if in.ClaimsInvestmentAllowance {
		schedules = append(schedules, "Investment Allowance brought-forward, current-year claim, utilisation and carry-forward schedule")
	}
	if in.ClaimsForeignTaxCredit {
		schedules = append(schedules, "Foreign tax credit or tax deducted at source computation by income source and country, or an eligible FTC pooling computation, with supporting evidence")
	}
	return schedules
}

func chooseForm(in CITInput) (string, []string) {
	var reasons []string
	if in.Revenue > FormCSRevenueLimit {
		reasons = append(reasons, "Revenue exceeds $5 million.")
	}
	if !in.OnlyIncomeAt17 {
		reasons = append(reasons, "Company has income not taxed at 17% (concessionary rates or tax-exempt income other than one-tier dividends).")
	}
	if in.ClaimsCarryBack {
		reasons = append(reasons, "Claims carry-back of current-year capital allowances or losses.")
	}
	if in.ClaimsGroupRelief {
		reasons = append(reasons, "Claims group relief.")
	}
	if in.ClaimsInvestmentAllowance {
		reasons = append(reasons, "Claims investment allowance.")
	}
	if in.ClaimsForeignTaxCredit {
		reasons = append(reasons, "Claims foreign tax credit or tax deducted at source.")
	}
	if len(reasons) > 0 {
		return "C", reasons
	}
	if in.Revenue <= FormCSLiteRevenueLimit {
		return "C-S Lite", []string{"Revenue is $200,000 or below, so the 6-field Form C-S (Lite) may be used (Form C-S is also allowed)."}
	}
	return "C-S", []string{"Revenue is $5 million or below and all Form C-S qualifying conditions are met."}
}
