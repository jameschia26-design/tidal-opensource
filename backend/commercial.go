package main

import (
	"strings"
)

// Commercial fields shared by invoice and quote forms; all money remains integer cents.
type commercialFields struct {
	Subject               string `json:"subject"`
	Salesperson           string `json:"salesperson"`
	Project               string `json:"project"`
	OrderNumber           string `json:"order_number"`
	PaymentTermsDays      int    `json:"payment_terms_days"`
	DiscountType          string `json:"discount_type"`
	DiscountCents         int64  `json:"discount_cents"`
	DiscountBasisPoints   int64  `json:"discount_basis_points"`
	ShippingCents         int64  `json:"shipping_cents"`
	AdjustmentCents       int64  `json:"adjustment_cents"`
	AdjustmentDescription string `json:"adjustment_description"`
	// Purchase order delivery contact (Xero: Attention, Telephone, Delivery instructions).
	Attention            string `json:"attention,omitempty"`
	Telephone            string `json:"telephone,omitempty"`
	DeliveryInstructions string `json:"delivery_instructions,omitempty"`
	// Purchase order document currency. Nothing posts from a PO, so the values travel to the bill.
	Currency    string `json:"currency,omitempty"`
	FXRateMicro int64  `json:"fx_rate_micro,omitempty"`
}

func commercialTotal(subtotal int64, c commercialFields) (int64, bool) {
	if !validTaxText(c.Subject, 200) || !validTaxText(c.Salesperson, 200) || !validTaxText(c.Project, 200) || !validTaxText(c.OrderNumber, 100) || !validTaxText(c.AdjustmentDescription, 200) || !validTaxText(c.Attention, 200) || !validTaxText(c.Telephone, 50) || !validTaxText(c.DeliveryInstructions, 500) || c.PaymentTermsDays < 0 || c.PaymentTermsDays > 365 || (c.Currency != "" && (len(c.Currency) != 3 || c.FXRateMicro <= 0)) {
		return 0, false
	}
	if c.DiscountType != "" && c.DiscountType != "flat" && c.DiscountType != "percent" {
		return 0, false
	}
	if c.DiscountCents < 0 || c.DiscountCents > subtotal || c.DiscountBasisPoints < 0 || c.DiscountBasisPoints > 10000 || c.ShippingCents < 0 || c.ShippingCents > maxMoneyCents || c.AdjustmentCents < -maxMoneyCents || c.AdjustmentCents > maxMoneyCents {
		return 0, false
	}
	discount := c.DiscountCents
	if c.DiscountType == "percent" {
		if c.DiscountCents != 0 {
			return 0, false
		}
		discount = (subtotal*c.DiscountBasisPoints + 5000) / 10000
	} else if c.DiscountBasisPoints != 0 {
		return 0, false
	}
	total := subtotal - discount + c.ShippingCents + c.AdjustmentCents
	return total, total > 0 && total <= maxMoneyCents
}

func documentNotes(notes, subject string) string {
	if strings.TrimSpace(subject) == "" {
		return notes
	}
	return "Subject: " + subject + "\n" + notes
}
