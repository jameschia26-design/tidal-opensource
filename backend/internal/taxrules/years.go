// Package taxrules holds Singapore tax rules as data and deterministic calculators.
// AI never does tax arithmetic: every number on a form comes from this package.
package taxrules

import (
	"fmt"
	"time"

	"tidal/backend/internal/money"
)

// Band is one slice of a tiered exemption or progressive tax schedule.
type Band struct {
	Upto money.Cents // upper bound of this band; 0 = unlimited
	BP   int64       // rate in basis points
}

// CITYear is the corporate income tax rule set for one Year of Assessment.
type CITYear struct {
	YA            int
	RateBP        int64
	PartialExempt []Band // partial tax exemption on normal chargeable income
	StartUpExempt []Band // tax exemption scheme for new start-up companies (first 3 YAs)
	RebateBP      int64  // CIT rebate as % of tax payable
	RebateCap     money.Cents
	// CashGrant is paid to companies with at least one local employee and is deducted from the rebate.
	CashGrant money.Cents
	// IPCDonationBP is the deduction multiplier for approved IPC donations (25000 = 250%).
	IPCDonationBP int64
	// SoftwareFilingExtension: filing via #SFFS software extends the 30 Nov deadline to 15 Dec.
	SoftwareFilingExtension bool
	Source                  string
	Notes                   []string
}

var (
	pte2020  = []Band{{Upto: 1000000, BP: 7500}, {Upto: 20000000, BP: 5000}}
	sute2020 = []Band{{Upto: 10000000, BP: 7500}, {Upto: 20000000, BP: 5000}}
)

const ircCalc = "IRAS Basic Corporate Income Tax Calculator"

// citYears is keyed by YA. Figures are checked against the IRAS calculator for that YA.
var citYears = map[int]CITYear{
	2020: {YA: 2020, RateBP: 1700, PartialExempt: pte2020, StartUpExempt: sute2020, RebateBP: 2500, RebateCap: 1500000, IPCDonationBP: 25000, Source: "IRAS: CIT rebate YA2020 25% capped $15,000"},
	2021: {YA: 2021, RateBP: 1700, PartialExempt: pte2020, StartUpExempt: sute2020, IPCDonationBP: 25000, Source: "IRAS: no CIT rebate for YA2021"},
	2022: {YA: 2022, RateBP: 1700, PartialExempt: pte2020, StartUpExempt: sute2020, IPCDonationBP: 25000, Source: "IRAS: no CIT rebate for YA2022"},
	2023: {YA: 2023, RateBP: 1700, PartialExempt: pte2020, StartUpExempt: sute2020, IPCDonationBP: 25000, SoftwareFilingExtension: true, Source: "IRAS: no CIT rebate for YA2023"},
	2024: {YA: 2024, RateBP: 1700, PartialExempt: pte2020, StartUpExempt: sute2020, RebateBP: 5000, RebateCap: 4000000, CashGrant: 200000, IPCDonationBP: 25000, SoftwareFilingExtension: true, Source: "Budget 2024: 50% rebate, total benefit capped $40,000 incl. $2,000 cash grant"},
	2025: {YA: 2025, RateBP: 1700, PartialExempt: pte2020, StartUpExempt: sute2020, RebateBP: 5000, RebateCap: 4000000, CashGrant: 200000, IPCDonationBP: 25000, SoftwareFilingExtension: true, Source: ircCalc + " YA2025"},
	2026: {YA: 2026, RateBP: 1700, PartialExempt: pte2020, StartUpExempt: sute2020, RebateBP: 5000, RebateCap: 4000000, CashGrant: 200000, IPCDonationBP: 25000, SoftwareFilingExtension: true, Source: ircCalc + " YA2026 (rebate enhanced 7 Apr 2026)",
		Notes: []string{"Cash grant requires CPF contributions for at least one local employee (not a shareholder-director) in 2025."}},
	2027: {YA: 2027, RateBP: 1700, PartialExempt: pte2020, StartUpExempt: sute2020, IPCDonationBP: 25000, SoftwareFilingExtension: true, Source: "No CIT rebate announced for YA2027 as of Oct 2026",
		Notes: []string{"Budget 2026: 400% deduction on up to $50,000 of qualifying AI expenditure for YA2027-YA2028 (claim as a further deduction)."}},
}

// CITFor returns the rule set for a YA.
func CITFor(ya int) (CITYear, error) {
	y, ok := citYears[ya]
	if !ok {
		return CITYear{}, fmt.Errorf("YA %d is not supported (supported: 2020-2027)", ya)
	}
	return y, nil
}

// SupportedCITYears lists YAs with rule tables.
func SupportedCITYears() []int { return []int{2020, 2021, 2022, 2023, 2024, 2025, 2026, 2027} }

// Form C-S eligibility thresholds (unchanged since YA2020).
const (
	FormCSRevenueLimit       money.Cents = 500000000 // $5m
	FormCSLiteRevenueLimit   money.Cents = 20000000  // $200k (also the Form B 2-line / 4-line statement threshold)
	CertifiedAccountsRevenue money.Cents = 50000000  // $500k: Form B needs certified accounts
	ECIWaiverRevenueLimit    money.Cents = 500000000 // $5m
	LossCarryBackCap         money.Cents = 10000000  // $100k
	LowValueAssetEach        money.Cents = 500000    // $5,000
	LowValueAssetYACap       money.Cents = 3000000   // $30,000 per YA
	RRCapPer3Years           money.Cents = 30000000  // $300,000 per fixed 3-year period
	MedicalCapBP             int64       = 100       // 1% of total remuneration
	MedicalCapEnhancedBP     int64       = 200       // 2% with PMBS / TMIS
)

// PITYear is the resident individual income tax schedule for one YA.
type PITYear struct {
	YA        int
	Bands     []Band // progressive bands on chargeable income
	RebateBP  int64
	RebateCap money.Cents
	ReliefCap money.Cents
	Source    string
}

var pit2017 = []Band{
	{Upto: 2000000, BP: 0}, {Upto: 3000000, BP: 200}, {Upto: 4000000, BP: 350}, {Upto: 8000000, BP: 700},
	{Upto: 12000000, BP: 1150}, {Upto: 16000000, BP: 1500}, {Upto: 20000000, BP: 1800}, {Upto: 24000000, BP: 1900},
	{Upto: 28000000, BP: 1950}, {Upto: 32000000, BP: 2000}, {Upto: 0, BP: 2200},
}

var pit2024 = []Band{
	{Upto: 2000000, BP: 0}, {Upto: 3000000, BP: 200}, {Upto: 4000000, BP: 350}, {Upto: 8000000, BP: 700},
	{Upto: 12000000, BP: 1150}, {Upto: 16000000, BP: 1500}, {Upto: 20000000, BP: 1800}, {Upto: 24000000, BP: 1900},
	{Upto: 28000000, BP: 1950}, {Upto: 32000000, BP: 2000}, {Upto: 50000000, BP: 2200}, {Upto: 100000000, BP: 2300},
	{Upto: 0, BP: 2400},
}

var pitYears = map[int]PITYear{
	2023: {YA: 2023, Bands: pit2017, ReliefCap: 8000000, Source: "IRAS individual income tax rates YA2017-YA2023"},
	2024: {YA: 2024, Bands: pit2024, RebateBP: 5000, RebateCap: 20000, ReliefCap: 8000000, Source: "IRAS: YA2024 rebate 50% capped $200"},
	2025: {YA: 2025, Bands: pit2024, RebateBP: 6000, RebateCap: 20000, ReliefCap: 8000000, Source: "IRAS: YA2025 rebate 60% capped $200"},
	2026: {YA: 2026, Bands: pit2024, ReliefCap: 8000000, Source: "IRAS: no personal income tax rebate listed for YA2026"},
	2027: {YA: 2027, Bands: pit2024, ReliefCap: 8000000, Source: "IRAS rates from YA2024; no rebate announced for YA2027"},
}

func PITFor(ya int) (PITYear, error) {
	y, ok := pitYears[ya]
	if !ok {
		return PITYear{}, fmt.Errorf("YA %d is not supported for personal income tax (supported: 2023-2027)", ya)
	}
	return y, nil
}

// GSTRateBP returns the standard GST rate in force on a date.
func GSTRateBP(d time.Time) int64 {
	switch {
	case !d.Before(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)):
		return 900
	case !d.Before(time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC)):
		return 800
	default:
		return 700
	}
}

// GSTRegistrationThreshold is the taxable-turnover threshold for compulsory GST registration.
const GSTRegistrationThreshold money.Cents = 100000000 // $1m

// Tiered applies a tiered exemption schedule to an amount.
func Tiered(amount money.Cents, bands []Band) money.Cents {
	var total, prev money.Cents
	for _, b := range bands {
		if amount <= prev {
			break
		}
		top := amount
		if b.Upto != 0 && b.Upto < top {
			top = b.Upto
		}
		total += (top - prev).MulRate(b.BP)
		prev = top
		if b.Upto == 0 {
			break
		}
	}
	return total
}

// YAForPeriodEnd returns the Year of Assessment for a basis period ending on d.
func YAForPeriodEnd(d time.Time) int { return d.Year() + 1 }
