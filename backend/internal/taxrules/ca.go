package taxrules

import (
	"fmt"
	"sort"
	"strings"

	"tidal/backend/internal/money"
)

// CA methods for plant and machinery (Sections 19 and 19A, Income Tax Act 1947).
const (
	CA1Year  = "s19a_1yr" // 100% in the YA of acquisition (computers, prescribed automation equipment)
	CA2Year  = "s19a_2yr" // 75% / 25%: only for assets acquired in the basis periods for YA2021, YA2022, YA2024
	CA3Year  = "s19a_3yr" // 33 1/3% over 3 YAs
	CALowVal = "lva"      // low-value assets <= $5,000 each, 100%, total capped at $30,000 per YA
	CAS19    = "s19"      // 20% initial allowance, then annual allowance over the prescribed working life
	CANone   = "none"     // no capital allowance (e.g. S-plate / Q-plate private cars)
)

// CAMethods describes each method for the UI and prompts.
var CAMethods = map[string]string{
	CA1Year:  "1-year write-off (100%) - computers and prescribed automation equipment",
	CA2Year:  "2-year write-off (75%/25%) - assets acquired in basis periods for YA2021, YA2022 or YA2024",
	CA3Year:  "3-year write-off (33 1/3% a year) - Section 19A(1)",
	CALowVal: "Low-value asset (cost <= $5,000) 100% write-off, capped at $30,000 per YA",
	CAS19:    "Section 19: 20% initial allowance + annual allowance over working life",
	CANone:   "No capital allowance (e.g. private passenger S-plate car)",
}

// Asset is one fixed asset in the register.
type Asset struct {
	ID                   string      `json:"id"`
	DisposalHistoryIssue string      `json:"disposalHistoryIssue,omitempty"`
	Description          string      `json:"description"`
	Category             string      `json:"category"`
	Cost                 money.Cents `json:"cost"`
	AcquiredYA           int         `json:"acquiredYa"`
	Method               string      `json:"method"`
	WorkingLife          int         `json:"workingLife,omitempty"`
	DisposedYA           int         `json:"disposedYa,omitempty"`
	DisposalProceeds     money.Cents `json:"disposalProceeds,omitempty"`
	// HirePurchase assets earn allowances on principal paid each year, which this calculator does not model.
	HirePurchase bool `json:"hirePurchase"`
	// ClaimHistory records capital allowances actually claimed for prior YAs. A
	// history is complete only through ClaimsConfirmedThroughYA; absent rows in
	// that confirmed range mean no claim was made for that YA.
	ClaimHistory             []CAClaim `json:"claimHistory,omitempty"`
	ClaimsConfirmedThroughYA int       `json:"claimsConfirmedThroughYa,omitempty"`
}

// CAClaim is the amount claimed for one asset in one Year of Assessment.
type CAClaim struct {
	YA                int         `json:"ya"`
	Amount            money.Cents `json:"amount"`
	EvidenceReference string      `json:"evidenceReference,omitempty"`
}

// ValidateMethod returns why an asset cannot use its chosen allowance method ("" if it can).
func ValidateMethod(a Asset) string {
	switch {
	case a.Method == CALowVal && a.Cost > LowValueAssetEach:
		return fmt.Sprintf("%s costs more than $5,000, so it cannot be written off as a low-value asset. Use the 3-year write-off.", a.Description)
	case a.Method == CA2Year && a.AcquiredYA != 2021 && a.AcquiredYA != 2022 && a.AcquiredYA != 2024:
		return fmt.Sprintf("%s: the 2-year write-off only applies to assets acquired in the basis period for YA2021, YA2022 or YA2024.", a.Description)
	}
	return ""
}

// lowValueOverflow returns assets that do not fit within the acquisition year's low-value cap.
func lowValueOverflow(assets []Asset) map[int]bool {
	used := map[int]money.Cents{}
	out := map[int]bool{}
	for i, a := range assets {
		if a.Method != CALowVal || ValidateMethod(a) != "" || a.HirePurchase || a.DisposedYA == a.AcquiredYA {
			continue
		}
		if used[a.AcquiredYA]+a.Cost > LowValueAssetYACap {
			out[i] = true
			continue
		}
		used[a.AcquiredYA] += a.Cost
	}
	return out
}

// CALine is one asset's position for the YA being computed.
type CALine struct {
	Asset                Asset       `json:"asset"`
	TWDVBroughtForward   money.Cents `json:"twdvBf"`
	Allowance            money.Cents `json:"allowance"`
	BalancingAllowance   money.Cents `json:"balancingAllowance"`
	BalancingCharge      money.Cents `json:"balancingCharge"`
	TWDVCarriedForward   money.Cents `json:"twdvCf"`
	ClaimHistoryComplete bool        `json:"claimHistoryComplete"`
}

type CASchedule struct {
	YA                 int         `json:"ya"`
	Lines              []CALine    `json:"lines"`
	TotalAllowance     money.Cents `json:"totalAllowance"`
	BalancingAllowance money.Cents `json:"balancingAllowance"`
	BalancingCharge    money.Cents `json:"balancingCharge"`
	LowValueTotal      money.Cents `json:"lowValueTotal"`
	Warnings           []string    `json:"warnings"`
}

// allowanceFor returns the allowance an asset earns in a YA, ignoring disposal.
func allowanceFor(a Asset, ya int) money.Cents {
	n := ya - a.AcquiredYA
	if n < 0 {
		return 0
	}
	switch a.Method {
	case CA1Year, CALowVal:
		if n == 0 {
			return a.Cost
		}
	case CA2Year:
		switch n {
		case 0:
			return a.Cost.MulRate(7500)
		case 1:
			return a.Cost - a.Cost.MulRate(7500)
		}
	case CA3Year:
		third := money.Cents((int64(a.Cost) + 1) / 3)
		switch n {
		case 0, 1:
			return third
		case 2:
			return a.Cost - 2*third
		}
	case CAS19:
		life := a.WorkingLife
		if life < 1 {
			life = 6
		}
		ia := a.Cost.MulRate(2000)
		aa := money.Cents(int64(a.Cost-ia) / int64(life))
		if n == 0 {
			return ia + aa
		}
		if n < life-1 {
			return aa
		}
		if n == life-1 { // last year takes the rounding remainder
			return a.Cost - ia - aa*money.Cents(life-1)
		}
	}
	return 0
}

// ComputeCA builds the capital allowance schedule for one YA from the asset register.
func ComputeCA(assets []Asset, ya int) (CASchedule, error) {
	s := CASchedule{YA: ya}
	overflow := lowValueOverflow(assets)
	var overCap []string
	for i, a := range assets {
		if _, ok := CAMethods[a.Method]; !ok {
			return s, fmt.Errorf("asset %q has unknown capital allowance method %q", a.Description, a.Method)
		}
		if a.AcquiredYA > ya || (a.DisposedYA != 0 && a.DisposedYA < ya) {
			continue
		}
		line := CALine{Asset: a}
		if a.DisposalHistoryIssue != "" {
			s.Warnings = append(s.Warnings, a.DisposalHistoryIssue)
			s.Lines = append(s.Lines, line)
			continue
		}
		// Assets that earn no allowance here are listed but take no part in the computation,
		// including on disposal.
		switch {
		case a.Method == CANone:
			line.ClaimHistoryComplete = a.AcquiredYA >= ya || a.ClaimsConfirmedThroughYA >= ya-1
			for _, claim := range a.ClaimHistory {
				if claim.YA < ya && claim.Amount > 0 {
					line.ClaimHistoryComplete = false
					s.Warnings = append(s.Warnings, fmt.Sprintf("%s has prior capital allowance claims but is marked ineligible. Reconcile the claims and disposal treatment before relying on this schedule.", a.Description))
					break
				}
			}
			s.Lines = append(s.Lines, line)
			continue
		case a.HirePurchase:
			s.Warnings = append(s.Warnings, fmt.Sprintf("%s is on hire purchase. Allowances follow the principal paid each year, which this calculator does not work out, so none were claimed. Ask your tax agent for the hire-purchase schedule.", a.Description))
			s.Lines = append(s.Lines, line)
			continue
		}
		if why := ValidateMethod(a); why != "" {
			s.Warnings = append(s.Warnings, why+" No allowance was claimed for it.")
			s.Lines = append(s.Lines, line)
			continue
		}
		if overflow[i] && a.AcquiredYA == ya {
			line.ClaimHistoryComplete = true
			overCap = append(overCap, a.Description)
			s.Lines = append(s.Lines, line)
			continue
		}
		var claimed money.Cents
		line.ClaimHistoryComplete = a.AcquiredYA >= ya || a.ClaimsConfirmedThroughYA >= ya-1
		if line.ClaimHistoryComplete {
			for _, claim := range a.ClaimHistory {
				if claim.YA >= a.AcquiredYA && claim.YA < ya {
					claimed += claim.Amount
				}
			}
		} else {
			// Keep a provisional projection visible, but never present it as an
			// actual brought-forward balance. The caller must block approval.
			for y := a.AcquiredYA; y < ya; y++ {
				claimed += allowanceFor(a, y)
			}
			s.Warnings = append(s.Warnings, fmt.Sprintf("%s: actual prior capital allowance claims are incomplete through YA %d; this tax written-down value is only an estimate. Confirm filed schedules and Notices of Assessment.", a.Description, ya-1))
		}
		line.TWDVBroughtForward = a.Cost - claimed
		allowance := allowanceFor(a, ya)
		if line.ClaimHistoryComplete && a.AcquiredYA < ya {
			var history []CAClaim
			for _, claim := range a.ClaimHistory {
				if claim.YA >= a.AcquiredYA && claim.YA < ya && claim.Amount > 0 {
					history = append(history, claim)
				}
			}
			sort.Slice(history, func(i, j int) bool { return history[i].YA < history[j].YA })
			valid := true
			switch a.Method {
			case CA3Year:
				for n, claim := range history {
					if n >= 3 || (n > 0 && history[n-1].YA == claim.YA) || claim.Amount != allowanceFor(a, a.AcquiredYA+n) {
						valid = false
					}
				}
				allowance = allowanceFor(a, a.AcquiredYA+len(history))
			case CA1Year:
				valid = len(history) == 0 || len(history) == 1 && history[0].Amount == a.Cost
				allowance = line.TWDVBroughtForward
			case CA2Year:
				for y := a.AcquiredYA; y < ya && y <= a.AcquiredYA+1; y++ {
					found := false
					for _, claim := range history {
						if claim.YA == y && claim.Amount == allowanceFor(a, y) {
							found = true
						}
					}
					valid = valid && found
				}
				for _, claim := range history {
					if claim.YA > a.AcquiredYA+1 {
						valid = false
					}
				}
			case CAS19:
				// ponytail: deferred Section 19 elections require separate recorded initial allowances.
				for y := a.AcquiredYA; y < ya; y++ {
					var actual money.Cents
					for _, claim := range history {
						if claim.YA == y {
							actual += claim.Amount
						}
					}
					if actual != allowanceFor(a, y) {
						valid = false
					}
				}
			case CALowVal:
				if claimed != 0 && claimed != a.Cost {
					valid = false
				}
				if claimed == 0 && a.DisposedYA != ya {
					// ponytail: deferred low-value elections need current-year cap allocation.
					valid = false
				}
			}
			if !valid {
				allowance = 0
				s.Warnings = append(s.Warnings, fmt.Sprintf("%s: the recorded allowance history or election cannot be calculated automatically; reconcile the filed schedule and use a supported method before approval.", a.Description))
			}
		}
		if a.DisposedYA == ya {
			// No allowance in the year of disposal; compare proceeds with tax written-down value.
			if claimed > 0 {
				diff := line.TWDVBroughtForward - a.DisposalProceeds
				if diff > 0 {
					line.BalancingAllowance = diff
				} else if diff < 0 {
					line.BalancingCharge = money.Min(-diff, claimed)
				}
			}
			line.TWDVCarriedForward = 0
		} else {
			line.Allowance = money.Min(allowance, line.TWDVBroughtForward)
			line.TWDVCarriedForward = line.TWDVBroughtForward - line.Allowance
		}
		if a.Method == CALowVal && a.AcquiredYA == ya {
			s.LowValueTotal += line.Allowance
		}
		s.TotalAllowance += line.Allowance
		s.BalancingAllowance += line.BalancingAllowance
		s.BalancingCharge += line.BalancingCharge
		s.Lines = append(s.Lines, line)
	}
	if len(overCap) > 0 {
		s.Warnings = append(s.Warnings, fmt.Sprintf("Low-value asset write-offs are capped at $30,000 per YA. No allowance was calculated for these assets: %s. Record the intended eligible method or defer the claim before approval.", strings.Join(overCap, ", ")))
	}
	return s, nil
}
