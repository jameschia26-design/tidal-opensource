package taxrules

import (
	"fmt"
	"strings"
	"time"

	"tidal/backend/internal/money"
)

// GST tax codes as used by IRAS-aligned accounting software.
var GSTCodes = map[string]string{
	// supplies
	"SR":    "Standard-rated supply",
	"DS":    "Deemed supply (e.g. gifts above $200, private use of business assets)",
	"SRRC":  "Imported services / low-value goods under reverse charge",
	"ZR":    "Zero-rated supply (export of goods, international services under s21(3))",
	"ES33":  "Regulation 33 exempt supply (e.g. incidental interest, exchange gains)",
	"ESN33": "Exempt supply not under Regulation 33 (financial services, residential property)",
	"OS":    "Out-of-scope supply (goods sold overseas never entering Singapore, private transactions)",
	// purchases
	"TX":   "Standard-rated purchase with claimable GST",
	"IM":   "Import of goods with GST paid to Singapore Customs",
	"ME":   "Import under Major Exporter Scheme / approved 3PL / other approved schemes",
	"ZP":   "Zero-rated purchase",
	"BL":   "Purchase with disallowed (blocked) input tax under Regulations 26/27",
	"TXRC": "Reverse charge purchase (input tax on imported services)",
	"EP":   "Exempt purchase",
	"OP":   "Out-of-scope purchase",
	"NR":   "Purchase from a supplier not registered for GST",
}

// GSTLine is one sale or purchase transaction (or a summarised total) for the period.
type GSTLine struct {
	SourceRef     string      `json:"sourceRef,omitempty"`
	Kind          string      `json:"kind"` // sale or purchase
	Code          string      `json:"code"`
	Description   string      `json:"description,omitempty"`
	Date          string      `json:"date,omitempty"`
	Reference     string      `json:"reference,omitempty"`
	Counterparty  string      `json:"counterparty,omitempty"`
	SourcePurpose string      `json:"sourcePurpose,omitempty"`
	Net           money.Cents `json:"net"`
	GST           money.Cents `json:"gst"`
}

type GSTInput struct {
	PeriodStart time.Time   `json:"periodStart"`
	PeriodEnd   time.Time   `json:"periodEnd"`
	Lines       []GSTLine   `json:"lines"`
	Revenue     money.Cents `json:"revenue"` // box 13
	// Yes/No questions (boxes 10-12, 14-17).
	TouristRefundClaim                  bool        `json:"touristRefundClaim"`
	BadDebtOrRCRefund                   bool        `json:"badDebtOrRcRefund"`
	PreRegistrationClaim                bool        `json:"preRegistrationClaim"`
	ImportedServicesRC                  bool        `json:"importedServicesRc"`
	MarketplaceRemote                   bool        `json:"marketplaceRemote"`
	RedelivererLVG                      bool        `json:"redelivererLvg"`
	SuppliedLVG                         bool        `json:"suppliedLvg"`
	TouristRefundAmount                 money.Cents `json:"touristRefundAmount"`
	BadDebtOrRCRefundAmount             money.Cents `json:"badDebtOrRcRefundAmount"`
	PreRegistrationPurchaseValue        money.Cents `json:"preRegistrationPurchaseValue"`
	PreRegistrationClaimAmount          money.Cents `json:"preRegistrationClaimAmount"`
	MarketplaceRemoteAmount             money.Cents `json:"marketplaceRemoteAmount"`
	RedelivererLVGAmount                money.Cents `json:"redelivererLvgAmount"`
	SuppliedLVGAmount                   money.Cents `json:"suppliedLvgAmount"`
	IGDSApplicable                      bool        `json:"igdsApplicable"`
	IGDSWorkingPaperReference           string      `json:"igdsWorkingPaperReference,omitempty"`
	PreRegistrationFirstReturnConfirmed bool        `json:"preRegistrationFirstReturnConfirmed"`
	ConditionalFieldsNote               string      `json:"conditionalFieldsNote,omitempty"`
}

type GSTBox struct {
	Box   string      `json:"box"`
	Label string      `json:"label"`
	Value money.Cents `json:"value"`
	YesNo *bool       `json:"yesNo,omitempty"`
}

type GSTResult struct {
	RateBP   int64       `json:"rateBp"`
	Boxes    []GSTBox    `json:"boxes"`
	NetGST   money.Cents `json:"netGst"`
	DueDate  time.Time   `json:"dueDate"`
	Warnings []string    `json:"warnings"`
}

// ComputeGSTF5 aggregates coded lines into the GST F5 boxes.
func ComputeGSTF5(in GSTInput) (GSTResult, error) {
	if in.PeriodEnd.Before(in.PeriodStart) {
		return GSTResult{}, fmt.Errorf("GST period end is before its start")
	}
	r := GSTResult{RateBP: GSTRateBP(in.PeriodEnd), DueDate: endOfMonth(in.PeriodEnd.AddDate(0, 0, 1))}
	if GSTRateBP(in.PeriodStart) != r.RateBP {
		r.Warnings = append(r.Warnings, "The period spans a GST rate change; check that each invoice uses the rate in force at its time of supply.")
	}
	if in.IGDSApplicable {
		if strings.TrimSpace(in.IGDSWorkingPaperReference) == "" {
			r.Warnings = append(r.Warnings, "IGDS applies; boxes 18–21 need a separate working paper and reference before filing.")
		} else {
			r.Warnings = append(r.Warnings, "IGDS boxes 18–21 are not computed here; complete the separate working paper referenced as "+strings.TrimSpace(in.IGDSWorkingPaperReference)+".")
		}
	}
	conditional := []struct {
		box    string
		claim  bool
		amount money.Cents
	}{
		{"10", in.TouristRefundClaim, in.TouristRefundAmount},
		{"11", in.BadDebtOrRCRefund, in.BadDebtOrRCRefundAmount},
		{"12", in.PreRegistrationClaim, in.PreRegistrationClaimAmount},
		{"15", in.MarketplaceRemote, in.MarketplaceRemoteAmount},
		{"16", in.RedelivererLVG, in.RedelivererLVGAmount},
		{"17", in.SuppliedLVG, in.SuppliedLVGAmount},
	}
	for _, field := range conditional {
		if field.amount < 0 {
			return GSTResult{}, fmt.Errorf("box %s amount cannot be negative", field.box)
		}
		if field.claim != (field.amount > 0) {
			r.Warnings = append(r.Warnings, fmt.Sprintf("Box %s answer and amount do not agree; review the conditional value before filing.", field.box))
		}
	}
	if (in.PreRegistrationPurchaseValue > 0) != (in.PreRegistrationClaimAmount > 0) {
		return GSTResult{}, fmt.Errorf("enter both the taxable purchase value and GST amount for pre-registration claims")
	}
	if in.PreRegistrationPurchaseValue < 0 {
		return GSTResult{}, fmt.Errorf("pre-registration purchase value cannot be negative")
	}
	var b1, b2, b3, b5, b6, b7, b9, blocked, rcValue money.Cents
	for _, l := range in.Lines {
		code := strings.ToUpper(strings.TrimSpace(l.Code))
		if _, ok := GSTCodes[code]; !ok {
			return GSTResult{}, fmt.Errorf("unknown GST code %q on %q", l.Code, l.Description)
		}
		if l.Kind != "sale" && l.Kind != "purchase" {
			return GSTResult{}, fmt.Errorf("GST line %q must be a sale or purchase", l.Description)
		}
		saleCode := code == "SR" || code == "DS" || code == "SRRC" || code == "ZR" || code == "ES33" || code == "ESN33" || code == "OS"
		if (l.Kind == "sale") != saleCode {
			return GSTResult{}, fmt.Errorf("GST code %s is not valid for a %s line", code, l.Kind)
		}
		if (l.Net < 0) != (l.GST < 0) && l.Net != 0 && l.GST != 0 {
			return GSTResult{}, fmt.Errorf("GST and net amounts must have the same sign for %q", l.Description)
		}
		if l.Net == 0 && l.GST != 0 {
			return GSTResult{}, fmt.Errorf("GST amount cannot be entered without a net amount for %q", l.Description)
		}
		switch code {
		case "SR", "DS":
			b1 += l.Net
			b6 += l.GST
		case "SRRC":
			b1 += l.Net
			b6 += l.GST
			rcValue += l.Net
		case "ZR":
			b2 += l.Net
		case "ES33", "ESN33":
			b3 += l.Net
		case "TX", "IM":
			b5 += l.Net
			b7 += l.GST
		case "TXRC":
			// IRAS e-Tax Guide "GST: Reverse Charge" (https://www.iras.gov.sg/media/docs/default-source/e-tax/gst-taxing-imported-services-by-way-of-reverse-charge-(2nd-edition).pdf)
			// para 11.1.1 table: "Value of imported services and LVG subject to reverse charge | Box 1, Box 5 and Box 14";
			// "Value of output tax to be accounted for on the imported services and LVG | Box 6";
			// "Value of input tax claimable on the imported services and LVG | Box 7".
			// Box 14 (https://www.iras.gov.sg/taxes/goods-services-tax-(gst)/filing-gst/completing-gst-returns):
			// "If you are required to account for GST under the reverse charge regime, indicate ‘Yes’ and state the value of imported services and/or low-value goods that is subject to GST in box 14. The same amount should also be included in the total value of standard-rated supplies reported in box 1."
			// Output tax is GST on the value at the period rate (guide para 4.1.3: "as if you were the supplier"). The line's GST is the input tax claimable after recovery rules.
			b1 += l.Net
			b5 += l.Net
			b6 += l.Net.MulRate(r.RateBP)
			b7 += l.GST
			rcValue += l.Net
		case "ZP":
			b5 += l.Net
		case "BL":
			// Purchases whose input tax is disallowed stay out of box 5, and their GST out of box 7.
			blocked += l.GST
		case "ME":
			// Imports with GST suspended are still taxable imports: box 5 as well as box 9.
			b5 += l.Net
			b9 += l.Net
		}
	}
	if in.ImportedServicesRC && rcValue == 0 {
		r.Warnings = append(r.Warnings, "Box 14 is marked Yes but no reverse-charge source line has a value; enter the transaction before filing.")
	}
	if in.MarketplaceRemoteAmount+in.RedelivererLVGAmount+in.SuppliedLVGAmount > b1 {
		return GSTResult{}, fmt.Errorf("boxes 15, 16 and 17 cannot exceed the standard-rated supplies reported in box 1")
	}
	b5 += in.PreRegistrationPurchaseValue
	b7 += in.TouristRefundAmount + in.BadDebtOrRCRefundAmount + in.PreRegistrationClaimAmount
	rate := r.RateBP
	if exp := b1.MulRate(rate); money.Abs(b6-exp) > money.Max(exp.MulRate(100), 100) {
		r.Warnings = append(r.Warnings, fmt.Sprintf("Output tax %s differs from %d%% of standard-rated supplies (%s); check GST amounts or mixed rates.", b6, rate/100, exp))
	}
	if b7 > b5.MulRate(rate)+100 {
		r.Warnings = append(r.Warnings, "Input tax claimed is higher than the standard rate applied to taxable purchases; check for errors.")
	}
	if blocked > 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("%s of GST on blocked purchases (club fees, medical, private car, family benefits) is not claimable; those purchases were left out of boxes 5 and 7.", blocked))
	}
	if b3 > 0 {
		r.Warnings = append(r.Warnings, "Exempt supplies were made: input tax may need partial-exemption apportionment unless the de minimis rule is met.")
	}
	r.NetGST = b6 - b7
	yn := func(v bool) *bool { return &v }
	r.Boxes = []GSTBox{
		{Box: "1", Label: "Total value of standard-rated supplies", Value: b1},
		{Box: "2", Label: "Total value of zero-rated supplies", Value: b2},
		{Box: "3", Label: "Total value of exempt supplies", Value: b3},
		{Box: "4", Label: "Total value of (1) + (2) + (3)", Value: b1 + b2 + b3},
		{Box: "5", Label: "Total value of taxable purchases", Value: b5},
		{Box: "6", Label: "Output tax due", Value: b6},
		{Box: "7", Label: "Less: Input tax and refunds claimed", Value: b7},
		{Box: "8", Label: "Equals: Net GST to be paid to / claimed from IRAS", Value: r.NetGST},
		{Box: "9", Label: "Total value of goods imported under MES / approved 3PL / other approved schemes", Value: b9},
		{Box: "10", Label: "Did you claim GST you refunded to tourists? · total claim", Value: in.TouristRefundAmount, YesNo: yn(in.TouristRefundClaim || in.TouristRefundAmount > 0)},
		{Box: "11", Label: "Bad debt relief / reverse-charge refund claims", Value: in.BadDebtOrRCRefundAmount, YesNo: yn(in.BadDebtOrRCRefund || in.BadDebtOrRCRefundAmount > 0)},
		{Box: "12", Label: "Pre-registration input tax claimed", Value: in.PreRegistrationClaimAmount, YesNo: yn(in.PreRegistrationClaim || in.PreRegistrationClaimAmount > 0)},
		{Box: "13", Label: "Revenue", Value: in.Revenue},
		{Box: "14", Label: "Did you import services and/or low-value goods subject to GST under reverse charge?", Value: rcValue, YesNo: yn(in.ImportedServicesRC || rcValue > 0)},
		{Box: "15", Label: "Remote services supplied by an electronic marketplace operator", Value: in.MarketplaceRemoteAmount, YesNo: yn(in.MarketplaceRemote || in.MarketplaceRemoteAmount > 0)},
		{Box: "16", Label: "Imported low-value goods supplied by a marketplace / redeliverer", Value: in.RedelivererLVGAmount, YesNo: yn(in.RedelivererLVG || in.RedelivererLVGAmount > 0)},
		{Box: "17", Label: "Own supply of imported low-value goods", Value: in.SuppliedLVGAmount, YesNo: yn(in.SuppliedLVG || in.SuppliedLVGAmount > 0)},
	}
	return r, nil
}

func endOfMonth(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month()+1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
}

// GSTRegistrationCheck flags when a non-registered business may need to register for GST.
// IRAS's retrospective test uses taxable turnover for a calendar year and the prospective test the
// next 12 months; accounting revenue for a financial year is only a pointer to both.
func GSTRegistrationCheck(revenue money.Cents, periodStart, periodEnd time.Time) string {
	if revenue <= GSTRegistrationThreshold {
		return ""
	}
	calendar := periodStart.Month() == time.January && periodStart.Day() == 1 && periodEnd.Month() == time.December && periodEnd.Day() == 31
	msg := fmt.Sprintf("Revenue for this financial year is %s, above $1 million. ", revenue)
	if calendar {
		msg += "If most of it is taxable supplies (not exempt or out of scope), taxable turnover for the calendar year exceeded $1 million and GST registration is compulsory: apply within 30 days of the year end."
	} else {
		msg += "Check taxable turnover for each calendar year (exempt and out-of-scope income does not count): if it exceeded $1 million, GST registration is compulsory within 30 days of that year end. Also register if you expect more than $1 million in the next 12 months."
	}
	return msg
}
