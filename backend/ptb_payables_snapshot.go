package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"tidal/backend/internal/ptb"
)

// A validated payables snapshot row is one open supplier document proven to exist in the backup with
// the same party, reference and positive amount. The open value is proven against backup arithmetic
// and its ageing buckets are recomputed from the backup (bill date + 30 days, bucket from as_of),
// never carried from the request; every other field is read from the backup, never from the request.
type validatedPTBPayableSnapshotRow struct {
	Party      uint32
	VendorCode string
	PartyName  string
	Reference  string
	BillDate   string
	DueDate    string
	Open       int64
	Buckets    [4]int64
}

var snapshotHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// validatePTBPayablesSnapshot proves every requested row against the backup, one document at a time.
// Anything missing, duplicated, ambiguous or arithmetically inconsistent rejects the whole snapshot,
// so a partially matched report can never be stored.
func validatePTBPayablesSnapshot(tables map[string][][]byte, asOf, sourceHash string, input []ptbPayablesSnapshotInput) ([]validatedPTBPayableSnapshotRow, error) {
	cutoff, ok := parseDay(asOf)
	if !ok || !snapshotHashPattern.MatchString(sourceHash) || len(input) == 0 || len(input) > 10000 {
		return nil, fmt.Errorf("invalid snapshot metadata")
	}

	vendorID := map[string]uint32{}
	vendorName := map[uint32]string{}
	for _, v := range tables["VENDOR.DAT"] {
		id := binary.LittleEndian.Uint32(v[1578:1582])
		code := strings.ToLower(strings.TrimSpace(ptb.Text(v[:21])))
		if _, dup := vendorID[code]; dup {
			return nil, fmt.Errorf("duplicate vendor code %q in backup", code)
		}
		vendorID[code] = id
		vendorName[id] = strings.TrimSpace(ptb.Text(v[37:77]))
	}

	docs, _, err := ptb.Classify(tables["JRNLHDR.DAT"], tables["JRNLROW.DAT"])
	if err != nil {
		return nil, err
	}
	_, controls, err := ptb.Controls(tables["JRNLHDR.DAT"], tables["JRNLROW.DAT"])
	if err != nil {
		return nil, err
	}
	header := map[uint32][]byte{}
	for _, h := range tables["JRNLHDR.DAT"] {
		header[binary.LittleEndian.Uint32(h[35:39])] = h
	}
	rowsByDoc := map[uint32][][]byte{}
	for _, r := range tables["JRNLROW.DAT"] {
		post := binary.LittleEndian.Uint32(r[4:8])
		rowsByDoc[post] = append(rowsByDoc[post], r)
	}

	type sourceBill struct {
		date   time.Time
		post   uint32
		amount int64
		name   string
	}
	bills := map[string]sourceBill{}
	for _, d := range docs {
		if d.Kind != ptb.Bill {
			continue
		}
		h := header[d.PostOrder]
		if h == nil {
			return nil, fmt.Errorf("bill %d has no header", d.PostOrder)
		}
		date, err := ptb.Date(h[15:19])
		if err != nil {
			return nil, err
		}
		// Payable control rows are credits on a bill, so the bill value is their absolute sum.
		var cents int64
		for _, r := range rowsByDoc[d.PostOrder] {
			if !controls[d.Party][binary.LittleEndian.Uint32(r[:4])] {
				continue
			}
			raw, err := ptb.PackedDecimal(r[53:65])
			if err != nil {
				return nil, err
			}
			cents += toCents(raw)
		}
		if cents < 0 {
			cents = -cents
		}
		if cents == 0 {
			continue
		}
		key := snapshotBillKey(d.Party, ptb.Text(h[82:103]))
		if _, dup := bills[key]; dup {
			return nil, fmt.Errorf("ambiguous bill reference %s in backup", key)
		}
		bills[key] = sourceBill{date: date, post: d.PostOrder, amount: cents, name: vendorName[d.Party]}
	}

	// Payments carry the bill they settle, so the open value can be computed from the backup instead of
	// being taken on trust: bill value less linked rows dated on or before the report date. Only rows
	// that name a bill are counted, and those rows must be dated by the cutoff, because a payment made
	// after the report date cannot reduce the balance the report shows.
	linked := map[uint32]int64{}
	headerDate := map[uint32]string{}
	for post, h := range header {
		if date, err := ptb.Date(h[15:19]); err == nil {
			headerDate[post] = date.Format("2006-01-02")
		}
	}
	for _, r := range tables["JRNLROW.DAT"] {
		target := binary.LittleEndian.Uint32(r[26:30])
		if target == 0 {
			continue
		}
		post := binary.LittleEndian.Uint32(r[4:8])
		if headerDate[post] == "" || headerDate[post] > cutoff.Format("2006-01-02") {
			continue
		}
		raw, err := ptb.PackedDecimal(r[53:65])
		if err != nil {
			return nil, err
		}
		linked[target] += toCents(raw)
	}
	openByBill := map[uint32]int64{}
	for _, b := range bills {
		openByBill[b.post] = b.amount - linked[b.post]
	}

	seen := map[string]bool{}
	out := make([]validatedPTBPayableSnapshotRow, 0, len(input))
	for _, in := range input {
		party, ok := vendorID[strings.ToLower(strings.TrimSpace(in.VendorCode))]
		if !ok {
			return nil, fmt.Errorf("unknown vendor code %q", in.VendorCode)
		}
		key := snapshotBillKey(party, in.Reference)
		if seen[key] {
			return nil, fmt.Errorf("duplicate snapshot row for %s", key)
		}
		bill, ok := bills[key]
		if !ok {
			return nil, fmt.Errorf("no bill in backup for %s", key)
		}
		seen[key] = true
		if !strings.EqualFold(bill.name, strings.TrimSpace(in.Party)) {
			return nil, fmt.Errorf("party name mismatch for %s", key)
		}
		// The declared open value must equal the backup's own arithmetic, so a mis-keyed amount or a
		// report from a different cutoff is refused rather than stored.
		if want := openByBill[bill.post]; in.OpenCents != want {
			return nil, fmt.Errorf("open amount %d for %s, backup says %d", in.OpenCents, key, want)
		}
		if len(in.Buckets) != 4 {
			return nil, fmt.Errorf("want 4 ageing buckets for %s", key)
		}
		// Buckets are recomputed from the backup, not carried: due = bill date + 30 days
		// (source report terms), bucket from as_of minus due. A misplaced report bucket
		// with a correct total is refused rather than stored.
		due := bill.date.AddDate(0, 0, 30)
		ageDays := int(cutoff.Sub(due).Hours() / 24)
		idx := 0
		switch {
		case ageDays <= 30:
			idx = 0
		case ageDays <= 60:
			idx = 1
		case ageDays <= 90:
			idx = 2
		default:
			idx = 3
		}
		var buckets [4]int64
		buckets[idx] = openByBill[bill.post]
		for i, amount := range in.Buckets {
			if amount != buckets[i] {
				return nil, fmt.Errorf("bucket %d %d for %s, backup says %d (due %s)", i, amount, key, buckets[i], due.Format("2006-01-02"))
			}
		}
		out = append(out, validatedPTBPayableSnapshotRow{
			Party:      party,
			VendorCode: in.VendorCode,
			PartyName:  bill.name,
			Reference:  normPTBReference(in.Reference),
			BillDate:   bill.date.Format("2006-01-02"),
			// The source report ages these suppliers on 30-day terms, so the due date shown is the
			// bill date plus 30 days. Buckets are recomputed from that due date above.
			DueDate: bill.date.AddDate(0, 0, 30).Format("2006-01-02"),
			Open:    in.OpenCents,
			Buckets: buckets,
		})
	}
	return out, nil
}

func snapshotBillKey(party uint32, reference string) string {
	return fmt.Sprintf("%d:%s", party, normPTBReference(reference))
}

func normPTBReference(s string) string {
	return strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "'")))
}

func insertPTBPayablesSnapshot(ctx context.Context, tx pgx.Tx, orgID, importID, userID int64, asOf, sourceHash string, rows []validatedPTBPayableSnapshotRow) error {
	var snapshotID int64
	if err := tx.QueryRow(ctx, `INSERT INTO ptb_payables_snapshots(org_id,import_id,as_of,source_sha256,row_count,created_by)
		VALUES($1,$2,$3,$4,$5,$6) RETURNING id`, orgID, importID, asOf, sourceHash, len(rows), userID).Scan(&snapshotID); err != nil {
		return err
	}
	for _, r := range rows {
		if _, err := tx.Exec(ctx, `INSERT INTO ptb_payables_snapshot_rows(snapshot_id,party_id,vendor_code,party_name,reference,bill_date,due_date,open_cents,bucket_0_30_cents,bucket_31_60_cents,bucket_61_90_cents,bucket_over_90_cents)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			snapshotID, r.Party, r.VendorCode, r.PartyName, r.Reference, r.BillDate, r.DueDate, r.Open, r.Buckets[0], r.Buckets[1], r.Buckets[2], r.Buckets[3]); err != nil {
			return err
		}
	}
	return nil
}
