package taxrules

import (
	"fmt"
	"time"

	"tidal/backend/internal/money"
)

// Relief codes with their statutory caps (resident individuals). 0 = no fixed cap (user-entered entitlement).
var ReliefCaps = map[string]money.Cents{
	"spouse":            200000,  // $2,000 (spouse income <= $4,000)
	"qcr":               0,       // qualifying child relief: amount depends on number/birth year of children
	"wmcr":              0,       // working mother's child relief
	"parent":            0,       // $9,000 staying with / $5,500 not staying with, per dependant
	"nsman":             500000,  // up to $5,000
	"cpf_self_employed": 3774000, // legacy combined CPF relief (before YA2026 rules); see cpfRelief
	// From YA2026 compulsory MediSave is relieved in full; voluntary CPF keeps the 37% / $37,740 limit.
	"cpf_medisave_compulsory": 0,
	"cpf_voluntary":           3774000,
	"life_insurance":          500000,  // up to $5,000 (only if CPF contributions are below $5,000)
	"srs":                     1530000, // $15,300 for citizens and PRs ($35,700 for foreigners)
	"cpf_topup":               1600000, // $8,000 own + $8,000 family members
	"course_fees":             550000,  // $5,500, last available for YA2025
	"other":                   0,
}

type PITInput struct {
	YA        int  `json:"ya"`
	Age       int  `json:"age"` // age on 31 Dec of the year before the YA
	Resident  bool `json:"resident"`
	Foreigner bool `json:"foreigner"`
	// Partner: business income is the partner's share of partnership profit (Form P allocation).
	Partner bool `json:"partner"`
	// Four-line statement for a sole proprietor or the partner's share of partnership profit.
	Revenue           money.Cents `json:"revenue"`
	GrossProfit       money.Cents `json:"grossProfit"`
	AllowableExpenses money.Cents `json:"allowableExpenses"`
	// PartnershipShare is the partner's share of the partnership's adjusted profit/loss (Form P allocation).
	PartnershipShare       money.Cents `json:"partnershipShare"`
	CapitalAllowances      money.Cents `json:"capitalAllowances"`
	BalancingCharge        money.Cents `json:"balancingCharge,omitempty"`
	CapitalAllowanceIssues []string    `json:"capitalAllowanceIssues,omitempty"`
	LossesBF               money.Cents `json:"lossesBf"`
	CapitalAllowancesBF    money.Cents `json:"capitalAllowancesBf"`

	// InterestExcluded is business-account interest moved out of trade income and treated as exempt
	// personal bank interest.
	InterestExcluded money.Cents `json:"interestExcluded,omitempty"`

	EmploymentIncome money.Cents `json:"employmentIncome"`
	NetRentalIncome  money.Cents `json:"netRentalIncome"`
	OtherIncome      money.Cents `json:"otherIncome"`
	IPCDonations     money.Cents `json:"ipcDonations"` // actual amount; deduction is 250%
	Reliefs          []Item      `json:"reliefs"`      // Label = relief code
}

type PITResult struct {
	YA                 int         `json:"ya"`
	AdjustedProfit     money.Cents `json:"adjustedProfit"`
	StatutoryBusiness  money.Cents `json:"statutoryBusinessIncome"`
	LossesCF           money.Cents `json:"lossesCf"`
	CACF               money.Cents `json:"caCf"`
	UsedLossesBF       money.Cents `json:"usedLossesBf"`
	UsedCABF           money.Cents `json:"usedCaBf"`
	CurrentYearLossCF  money.Cents `json:"currentYearLossCf"`
	CurrentYearCACF    money.Cents `json:"currentYearCaCf"`
	TotalIncome        money.Cents `json:"totalIncome"`
	Donations          money.Cents `json:"donations"`
	AssessableIncome   money.Cents `json:"assessableIncome"`
	EarnedIncomeRelief money.Cents `json:"earnedIncomeRelief"`
	Reliefs            []Item      `json:"reliefs"`
	TotalReliefs       money.Cents `json:"totalReliefs"`
	ChargeableIncome   money.Cents `json:"chargeableIncome"`
	GrossTax           money.Cents `json:"grossTax"`
	Rebate             money.Cents `json:"rebate"`
	NetTax             money.Cents `json:"netTax"`
	FormType           string      `json:"formType"`
	// StatementLines is the business income statement Form B needs: 2 lines up to $200,000 revenue,
	// 4 lines above that (certified accounts are also needed from $500,000).
	StatementLines int       `json:"statementLines"`
	FilingDue      time.Time `json:"filingDue"`
	Warnings       []string  `json:"warnings"`
	Source         string    `json:"source"`
}

// ComputePIT computes individual income tax for the supported Form B cases.
func ComputePIT(in PITInput) (PITResult, error) {
	y, err := PITFor(in.YA)
	if err != nil {
		return PITResult{}, err
	}
	r := PITResult{YA: in.YA, Source: y.Source, FilingDue: time.Date(in.YA, 4, 18, 0, 0, 0, 0, time.UTC)}
	if in.Age < 0 || in.Age > 120 {
		return PITResult{}, fmt.Errorf("age must be between 0 and 120")
	}
	if in.BalancingCharge < 0 || in.CapitalAllowances < 0 || in.CapitalAllowancesBF < 0 || in.LossesBF < 0 {
		return PITResult{}, fmt.Errorf("capital allowances, balancing charges and brought-forward losses cannot be negative")
	}
	r.Warnings = append(r.Warnings, in.CapitalAllowanceIssues...)
	r.AdjustedProfit = rd(in.GrossProfit-in.AllowableExpenses) + rd(in.PartnershipShare) + rd(in.BalancingCharge)
	hasBusiness := in.Revenue != 0 || in.GrossProfit != 0 || in.AllowableExpenses != 0 || in.PartnershipShare != 0 || in.BalancingCharge != 0
	r.FormType = "B1"
	if hasBusiness {
		r.FormType = "B"
		r.StatementLines = 2
		if in.Revenue > FormCSLiteRevenueLimit {
			r.StatementLines = 4
		}
		if in.Revenue >= CertifiedAccountsRevenue {
			r.Warnings = append(r.Warnings, "Revenue is $500,000 or more: submit a certified statement of accounts and a tax computation with Form B.")
		}
		if in.Partner && in.PartnershipShare == 0 {
			r.Warnings = append(r.Warnings, "Enter your share of the partnership's adjusted profit or loss from its Form P allocation; no partnership income is included until you do.")
		}
	}
	if in.InterestExcluded > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%s of interest was treated as exempt personal bank interest. If it is not from a bank or finance company in Singapore, add it under other income.", in.InterestExcluded))
	}
	// Current-year capital allowances are set off against business income first and then against
	// other income; losses follow. Anything unabsorbed carries forward.
	avail := money.Max(r.AdjustedProfit, 0)
	caBF := rd(in.CapitalAllowancesBF)
	caCurrent := rd(in.CapitalAllowances)
	ca := caBF + caCurrent
	caUsed := money.Min(ca, avail)
	avail -= caUsed
	other := rd(in.EmploymentIncome) + money.Max(rd(in.NetRentalIncome), 0) + rd(in.OtherIncome)
	caOther := money.Min(ca-caUsed, other)
	other -= caOther
	r.CACF = ca - caUsed - caOther
	r.UsedCABF = money.Min(caBF, caUsed+caOther)
	r.CurrentYearCACF = r.CACF - (caBF - r.UsedCABF)
	lossesBF := rd(in.LossesBF)
	currentLoss := money.Max(-r.AdjustedProfit, 0)
	lossPool := lossesBF + currentLoss
	// Current-year trade losses and losses b/f can be set off against other income.
	lossUsed := money.Min(lossPool, avail+other)
	r.LossesCF = lossPool - lossUsed
	r.UsedLossesBF = money.Min(lossesBF, lossUsed)
	r.CurrentYearLossCF = currentLoss - (lossUsed - r.UsedLossesBF)
	r.StatutoryBusiness = avail
	r.TotalIncome = avail + other - lossUsed
	r.Donations = rd(in.IPCDonations.MulRate(25000))
	r.AssessableIncome = money.Max(r.TotalIncome-r.Donations, 0)

	if in.Resident {
		earned := rd(in.EmploymentIncome) + money.Max(r.AdjustedProfit, 0)
		eir := money.Cents(100000)
		switch {
		case in.Age >= 60:
			eir = 800000
		case in.Age >= 55:
			eir = 600000
		}
		r.EarnedIncomeRelief = money.Min(eir, earned)
	}
	total := r.EarnedIncomeRelief
	// Net trade income: adjusted profit less capital allowances and trade losses brought forward.
	nti := money.Max(r.AdjustedProfit-ca-rd(in.LossesBF), 0)
	cpf, cpfWarn := cpfRelief(in.YA, nti, in.Reliefs)
	r.Warnings = append(r.Warnings, cpfWarn...)
	for _, it := range cpf {
		r.Reliefs = append(r.Reliefs, it)
		total += it.Amount
	}
	// Aggregate repeated relief rows before applying each statutory limit. Applying a per-row
	// limit lets duplicate SRS/NSman/etc. entries exceed the taxpayer's actual entitlement.
	byRelief := make(map[string]Item, len(in.Reliefs))
	var reliefOrder []string
	for _, it := range in.Reliefs {
		_, known := ReliefCaps[it.Label]
		if !known {
			return PITResult{}, fmt.Errorf("unknown relief %q", it.Label)
		}
		if it.Amount < 0 {
			return PITResult{}, fmt.Errorf("%s relief cannot be negative", it.Label)
		}
		if _, ok := byRelief[it.Label]; !ok {
			reliefOrder = append(reliefOrder, it.Label)
			byRelief[it.Label] = it
		} else {
			cur := byRelief[it.Label]
			cur.Amount += it.Amount
			byRelief[it.Label] = cur
		}
	}
	for _, code := range reliefOrder {
		it := byRelief[code]
		capAmt := ReliefCaps[code]
		amt := rd(it.Amount)
		if it.Label == "course_fees" && in.YA >= 2026 {
			r.Warnings = append(r.Warnings, "Course fees relief has lapsed from YA2026 and was not applied.")
			continue
		}
		if cpfCodes[it.Label] {
			continue // worked out together in cpfRelief
		}
		if it.Label == "srs" && in.Foreigner {
			capAmt = 3570000
		}
		if capAmt > 0 && amt > capAmt {
			r.Warnings = append(r.Warnings, fmt.Sprintf("%s relief capped at %s.", it.Label, capAmt))
			amt = capAmt
		}
		r.Reliefs = append(r.Reliefs, Item{Label: it.Label, Amount: amt, Reason: it.Reason})
		total += amt
	}
	if !in.Resident {
		if len(r.Reliefs) > 0 || r.EarnedIncomeRelief > 0 {
			return PITResult{}, fmt.Errorf("non-residents cannot claim personal reliefs; remove relief entries and prepare a supported non-resident case")
		}
		if in.EmploymentIncome != 0 || in.NetRentalIncome != 0 || in.OtherIncome != 0 {
			return PITResult{}, fmt.Errorf("non-resident Form B currently supports Singapore trade income only; employment, rental and other income need a separate assessment")
		}
		if in.IPCDonations != 0 {
			return PITResult{}, fmt.Errorf("non-resident donation deductions require a separate eligibility review and are not supported in this Form B calculation")
		}
		r.AssessableIncome = r.TotalIncome
		rate := int64(2400)
		if in.YA < 2024 {
			rate = 2200
		}
		r.GrossTax = money.Max(r.AssessableIncome, 0).MulRate(rate)
		r.NetTax = r.GrossTax
		r.ChargeableIncome = r.AssessableIncome
		r.Warnings = append(r.Warnings, fmt.Sprintf("Non-resident Singapore trade income is taxed at %g%% for YA%d; personal reliefs are not applied.", float64(rate)/100, in.YA))
		return r, nil
	}
	if total > y.ReliefCap {
		r.Warnings = append(r.Warnings, fmt.Sprintf("Total reliefs %s exceed the $80,000 personal relief cap; capped.", total))
		total = y.ReliefCap
	}
	r.TotalReliefs = total
	r.ChargeableIncome = money.Max(r.AssessableIncome-total, 0)
	r.GrossTax = progressive(r.ChargeableIncome, y.Bands)
	if y.RebateBP > 0 {
		r.Rebate = money.Min(r.GrossTax.MulRate(y.RebateBP), y.RebateCap)
	}
	r.NetTax = r.GrossTax - r.Rebate
	if hasBusiness && r.AdjustedProfit > 600000 {
		r.Warnings = append(r.Warnings, "Net trade income exceeds $6,000: MediSave contributions are compulsory for the self-employed.")
	}
	return r, nil
}

var cpfCodes = map[string]bool{"cpf_self_employed": true, "cpf_medisave_compulsory": true, "cpf_voluntary": true}

// cpfRelief applies IRAS's CPF relief for the self-employed. Before YA2026, MediSave and voluntary
// contributions share one limit: the lower of 37% of net trade income and $37,740. From YA2026,
// compulsory MediSave is relieved in full and voluntary contributions only use what is left of that
// limit (none if compulsory MediSave already exceeds 37% of net trade income).
func cpfRelief(ya int, nti money.Cents, reliefs []Item) ([]Item, []string) {
	var compulsory, voluntary, legacy money.Cents
	for _, it := range reliefs {
		switch it.Label {
		case "cpf_medisave_compulsory":
			compulsory += rd(it.Amount)
		case "cpf_voluntary":
			voluntary += rd(it.Amount)
		case "cpf_self_employed":
			legacy += rd(it.Amount)
		}
	}
	if compulsory+voluntary+legacy == 0 {
		return nil, nil
	}
	limit := money.Min(nti.MulRate(3700), ReliefCaps["cpf_voluntary"])
	var out []Item
	var warn []string
	capped := func(label string, amt, lim money.Cents) {
		lim = money.Max(lim, 0)
		if amt > lim {
			warn = append(warn, fmt.Sprintf("%s relief capped at %s.", label, lim))
			amt = lim
		}
		out = append(out, Item{Label: label, Amount: amt})
	}
	if ya < 2026 {
		capped("cpf_self_employed", compulsory+voluntary+legacy, limit)
		return out, warn
	}
	if legacy > 0 {
		warn = append(warn, "From YA2026 compulsory MediSave and voluntary CPF contributions are relieved differently. Enter them separately; the combined amount was treated as voluntary.")
		voluntary += legacy
	}
	if compulsory > 0 {
		out = append(out, Item{Label: "cpf_medisave_compulsory", Amount: compulsory})
	}
	if voluntary > 0 {
		capped("cpf_voluntary", voluntary, limit-compulsory)
	}
	return out, warn
}

func progressive(ci money.Cents, bands []Band) money.Cents {
	var tax, prev money.Cents
	for _, b := range bands {
		top := ci
		if b.Upto != 0 && b.Upto < top {
			top = b.Upto
		}
		if top > prev {
			tax += (top - prev).MulRate(b.BP)
		}
		if b.Upto == 0 || ci <= b.Upto {
			break
		}
		prev = b.Upto
	}
	return tax
}

// EntityTypePartnership reports whether this input is for a partner (set by the caller via Partner).
func (in PITInput) EntityTypePartnership() bool { return in.Partner }

// AddCapitalAllowanceSchedule keeps taxable balancing charges separate from deductions.
func (in *PITInput) AddCapitalAllowanceSchedule(ca CASchedule) {
	in.CapitalAllowanceIssues = append(in.CapitalAllowanceIssues, ca.Warnings...)
	in.CapitalAllowances += ca.TotalAllowance + ca.BalancingAllowance
	in.BalancingCharge += ca.BalancingCharge
}
