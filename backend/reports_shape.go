package main

// Report shapes for the two ledger statements. The broad account type still drives posting; the
// account class decides which section a balance lands in, so the statements have Xero's order:
// revenue, cost of sales, gross profit, operating expenses, operating profit, other income and
// expense, net profit for the P&L; current, fixed, inventory, prepayment, bank and liability
// sections plus equity and net assets for the balance sheet.

type reportLine struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	Class       string `json:"class"`
	AmountCents int64  `json:"amount_cents"`
	Computed    bool   `json:"computed,omitempty"`
}

type reportSection struct {
	Key        string       `json:"key"`
	Label      string       `json:"label"`
	Lines      []reportLine `json:"lines"`
	TotalCents int64        `json:"total_cents"`
}

// sectionKey reports the section a class belongs to, and whether its balance is debit-natured.
// An empty class can only come from a row written before migration 120, so fall back to the type.
func sectionKey(b balRow) string {
	if b.Class != "" {
		return b.Class
	}
	return defaultAccountClass(b.Type, b.Code)
}

// creditAmount signs a balance the way Xero presents the section: revenue, liability and equity are
// credit-positive, assets and expenses debit-positive.
func creditAmount(b balRow) int64 {
	switch b.Type {
	case "revenue", "liability", "equity":
		return b.Credit - b.Debit
	}
	return b.Debit - b.Credit
}

// pnlSections groups every revenue and expense account into the five profit and loss sections.
func pnlSections(rows []balRow) []reportSection {
	sections := []reportSection{
		{Key: "revenue", Label: "Revenue", Lines: []reportLine{}},
		{Key: "cost_of_sales", Label: "Cost of sales", Lines: []reportLine{}},
		{Key: "operating_expenses", Label: "Operating expenses", Lines: []reportLine{}},
		{Key: "other_income", Label: "Other income", Lines: []reportLine{}},
		{Key: "other_expense", Label: "Other expense", Lines: []reportLine{}},
	}
	index := map[string]int{"sales": 0, "revenue": 0, "cost_of_sales": 1, "expense": 2,
		"other_income": 3, "other_expense": 4}
	for _, b := range rows {
		if b.Type != "revenue" && b.Type != "expense" {
			continue
		}
		i, ok := index[sectionKey(b)]
		if !ok {
			continue
		}
		amt := creditAmount(b)
		// Xero's balance sheet omits accounts with no balance, so a superseded GST input account never
		// adds a second GST line beside the single control account.
		if amt == 0 {
			continue
		}
		sections[i].Lines = append(sections[i].Lines, reportLine{Code: b.Code, Name: b.Name, Class: sectionKey(b), AmountCents: amt})
		sections[i].TotalCents += amt
	}
	return sections
}

// pnlBody builds the response map shared by the current period and the previous-year column.
func pnlBody(rows []balRow) map[string]any {
	sections := pnlSections(rows)
	// Section order matches pnlSections: revenue, cost of sales, operating expenses, other income,
	// other expense.
	var lines []reportLine
	for _, s := range sections {
		lines = append(lines, s.Lines...)
	}
	rev := sections[0].TotalCents
	cogs := sections[1].TotalCents
	opex := sections[2].TotalCents
	otherIncome := sections[3].TotalCents
	otherExpense := sections[4].TotalCents
	gross := rev - cogs
	operating := gross - opex
	net := operating + otherIncome - otherExpense
	return map[string]any{
		"sections": sections, "lines": lines,
		"revenue_cents": rev, "cost_of_sales_cents": cogs, "gross_profit_cents": gross,
		"operating_expenses_cents": opex, "operating_profit_cents": operating,
		"other_income_cents": otherIncome, "other_expense_cents": otherExpense,
		// expense_cents keeps its old meaning: the cost the company spent to earn the revenue.
		"expense_cents": cogs + opex, "profit_cents": net,
	}
}

// balanceSections groups every asset, liability and equity account into Xero's balance sheet
// sections. Retained earnings keeps its own class inside equity, and the profit not yet closed to it
// is appended as one computed line so the two are never counted twice: a closing entry zeroes the
// revenue and expense accounts the computed line is built from.
func balanceSections(rows []balRow) (sections []reportSection, accumulated int64) {
	order := []struct{ key, label string }{
		{"bank", "Bank"}, {"current_assets", "Current assets"}, {"fixed_assets", "Fixed assets"},
		{"inventory", "Inventory"}, {"prepayments", "Prepayments"}, {"non_current_assets", "Non-current assets"},
		{"current_liabilities", "Current liabilities"}, {"non_current_liabilities", "Non-current liabilities"},
		{"equity", "Equity"},
	}
	classKey := map[string]string{
		"bank": "bank", "current_asset": "current_assets", "fixed_asset": "fixed_assets",
		"inventory": "inventory", "prepayment": "prepayments", "non_current_asset": "non_current_assets",
		"current_liability": "current_liabilities", "non_current_liability": "non_current_liabilities",
		"equity": "equity", "retained_earnings": "equity",
	}
	index := map[string]int{}
	for i, s := range order {
		sections = append(sections, reportSection{Key: s.key, Label: s.label, Lines: []reportLine{}})
		index[s.key] = i
	}
	for _, b := range rows {
		switch b.Type {
		case "revenue":
			accumulated += b.Credit - b.Debit
			continue
		case "expense":
			accumulated -= b.Debit - b.Credit
			continue
		}
		i, ok := index[classKey[sectionKey(b)]]
		if !ok {
			continue
		}
		amt := creditAmount(b)
		sections[i].Lines = append(sections[i].Lines, reportLine{Code: b.Code, Name: b.Name, Class: sectionKey(b), AmountCents: amt})
		sections[i].TotalCents += amt
	}
	equity := &sections[index["equity"]]
	equity.Lines = append(equity.Lines, reportLine{
		Name: "Accumulated profit (not yet closed to retained earnings)", Class: "retained_earnings",
		AmountCents: accumulated, Computed: true,
	})
	equity.TotalCents += accumulated
	return sections, accumulated
}

// balanceSheetBody builds the response map shared by the current date and the previous-year column.
func balanceSheetBody(rows []balRow) map[string]any {
	sections, _ := balanceSections(rows)
	var lines []reportLine
	var assets, liabilities int64
	for _, s := range sections {
		lines = append(lines, s.Lines...)
		switch s.Key {
		case "bank", "current_assets", "fixed_assets", "inventory", "prepayments", "non_current_assets":
			assets += s.TotalCents
		case "current_liabilities", "non_current_liabilities":
			liabilities += s.TotalCents
		}
	}
	equity := int64(0)
	for _, s := range sections {
		if s.Key == "equity" {
			equity = s.TotalCents
		}
	}
	return map[string]any{
		"sections": sections, "lines": lines,
		"assets_cents": assets, "liabilities_cents": liabilities, "equity_cents": equity,
		"net_assets_cents": assets - liabilities, "balanced": assets == liabilities+equity,
	}
}
