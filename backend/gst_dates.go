package main

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// rowQueryer is satisfied by both the connection pool and a transaction.
type rowQueryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// gstRegisteredOn reports whether the company was GST-registered on a date. Dated registration history
// wins where it exists; rows without history (legacy registrations) fall back to the stored flag.
func gstRegisteredOn(ctx context.Context, q rowQueryer, orgID int64, on string) (bool, error) {
	var registered bool
	err := q.QueryRow(ctx, `SELECT CASE WHEN EXISTS(SELECT 1 FROM gst_registrations WHERE org_id=$1)
		THEN EXISTS(SELECT 1 FROM gst_registrations WHERE org_id=$1
			AND effective_from<=$2::date AND (effective_to IS NULL OR effective_to>=$2::date))
		ELSE gst_registered END FROM organizations WHERE id=$1`, orgID, on).Scan(&registered)
	return registered, err
}

// gstRegisteredInPeriod reports whether the company was registered at any point in the period, so a
// return for a past registered period is still accepted after deregistration.
func gstRegisteredInPeriod(ctx context.Context, q rowQueryer, orgID int64, start, end string) (bool, error) {
	var registered bool
	err := q.QueryRow(ctx, `SELECT CASE WHEN EXISTS(SELECT 1 FROM gst_registrations WHERE org_id=$1)
		THEN EXISTS(SELECT 1 FROM gst_registrations WHERE org_id=$1
			AND effective_from<=$3::date AND (effective_to IS NULL OR effective_to>=$2::date))
		ELSE gst_registered END FROM organizations WHERE id=$1`, orgID, start, end).Scan(&registered)
	return registered, err
}

// gstRateBPOn is the statutory rate for a date; zero when no rate covers it.
func gstRateBPOn(ctx context.Context, q rowQueryer, on string) (int64, error) {
	var rate int64
	err := q.QueryRow(ctx, `SELECT COALESCE((SELECT rate_bp FROM gst_rates WHERE effective_from<=$1::date ORDER BY effective_from DESC LIMIT 1),0)`, on).Scan(&rate)
	return rate, err
}
