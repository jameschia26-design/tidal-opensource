package main

// Peachtree .ptb import, step one: accept the upload, validate it hard, decode it and stage the
// records against the company. Nothing is posted to the books here; the preview and posting steps
// read what this leaves behind.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"tidal/backend/internal/ptb"
)

const (
	ptbMaxUploadBytes = 64 << 20  // the reference backup is 24 MB
	ptbMaxEntryBytes  = 64 << 20  // per archive entry, before decompression
	ptbMaxTotalBytes  = 256 << 20 // all decoded entries together
	ptbMaxEntries     = 4000      // the reference backup has 527
	ptbMaxRecords     = 500_000   // the reference backup has 30,000
)

// uploadPTBImport stages a backup for import. Repeated uploads of the same file are refused so a
// double click cannot create a second import.
func (a *app) uploadPTBImport(w http.ResponseWriter, r *http.Request, u currentUser) {
	org, ok := parseOrgID(w, r.URL.Query().Get("org_id"))
	if !ok {
		return
	}
	access, ok := a.orgFor(w, r, u.ID, org, true)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, ptbMaxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		writeError(w, http.StatusBadRequest, "invalid_upload")
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_upload")
		return
	}
	defer file.Close()
	name := filepath.Base(strings.ReplaceAll(header.Filename, "\\", "/"))
	if !strings.EqualFold(filepath.Ext(name), ".ptb") {
		writeError(w, http.StatusUnprocessableEntity, "not_a_ptb_file")
		return
	}

	dir, err := os.MkdirTemp("", "ptb-import-")
	if err != nil {
		serverError(w, err)
		return
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "backup.ptb")
	size, sum, err := copyUpload(file, path)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_upload")
		return
	}

	archive, err := ptb.Open(path)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "not_a_ptb_backup")
		return
	}
	defer archive.Close()

	names := archive.Names()
	if len(names) == 0 || len(names) > ptbMaxEntries {
		writeError(w, http.StatusUnprocessableEntity, "not_a_ptb_backup")
		return
	}
	seen := map[string]bool{}
	for _, name := range names {
		key := strings.ToUpper(name)
		if !safeEntryName(name) || seen[key] {
			writeError(w, http.StatusUnprocessableEntity, "invalid_archive_path")
			return
		}
		seen[key] = true
	}

	ctx := r.Context()
	var existing int64
	err = a.db.QueryRow(ctx, `SELECT id FROM ptb_imports WHERE org_id=$1 AND sha256=$2`, access.ID, sum).Scan(&existing)
	if err == nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "already_imported", "import_id": existing})
		return
	}

	if !errors.Is(err, pgx.ErrNoRows) {
		serverError(w, err)
		return
	}
	decoded, skipped, total, err := decodeTables(archive, names)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_backup")
		return
	}
	if len(decoded) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "no_supported_tables")
		return
	}
	counts := map[string]any{"records": recordCounts(decoded), "skipped": skipped, "bytes": total}
	exceptions := make([]map[string]string, 0, len(skipped))
	for _, key := range sortedMapKeys(skipped) {
		exceptions = append(exceptions, map[string]string{"table": key, "reason": skipped[key]})
	}
	tx, err := a.db.Begin(ctx)
	if err != nil {
		serverError(w, err)
		return
	}
	defer tx.Rollback(ctx)

	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO ptb_imports (org_id, created_by, filename, sha256, size_bytes, record_counts, exceptions)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT (org_id, sha256) DO NOTHING
		RETURNING id`, access.ID, u.ID, name, sum, size, counts, exceptions).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusConflict, "already_imported")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	for _, table := range sortedKeys(decoded) {
		rows := decoded[table]
		ordinals := make([]int32, len(rows))
		payloads := make([][]byte, len(rows))
		for i, row := range rows {
			ordinals[i] = int32(i + 1)
			payloads[i] = row
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO ptb_import_records (import_id, table_name, ordinal, payload)
			SELECT $1, $2, o, p FROM unnest($3::int[], $4::bytea[]) AS x(o, p)`,
			id, table, ordinals, payloads); err != nil {
			serverError(w, err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id, "status": "staged", "filename": name, "sha256": sum,
		"size_bytes": size, "records": counts["records"], "skipped": skipped,
	})
}

// copyUpload writes the upload to disk and hashes it in one pass, refusing anything over the cap.
func copyUpload(src io.Reader, path string) (int64, string, error) {
	f, err := os.Create(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(src, ptbMaxUploadBytes+1))
	if err != nil {
		return 0, "", err
	}
	if n == 0 || n > ptbMaxUploadBytes {
		return 0, "", fmt.Errorf("upload of %d bytes is outside the allowed size", n)
	}
	if err := f.Sync(); err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(hash.Sum(nil)), nil
}

// decodeTables reads every Btrieve data file it can. Files it cannot read are reported with a
// reason rather than silently dropped.
func decodeTables(archive *ptb.Archive, names []string) (map[string][][]byte, map[string]string, int, error) {
	decoded := map[string][][]byte{}
	skipped := map[string]string{}
	total, records := 0, 0
	required := map[string]bool{"CHART.DAT": false, "CUSTOMER.DAT": false, "VENDOR.DAT": false, "JRNLHDR.DAT": false, "JRNLROW.DAT": false}
	for _, name := range names {
		limit := int64(ptbMaxTotalBytes - total)
		if limit > ptbMaxEntryBytes {
			limit = ptbMaxEntryBytes
		}
		raw, err := archive.ReadLimit(name, limit)
		if err != nil {
			return nil, nil, 0, err
		}
		total += len(raw)
		if !strings.EqualFold(filepath.Ext(name), ".dat") {
			continue
		}
		rows, err := ptb.RecordsLimit(raw, ptbMaxRecords-records, ptbMaxTotalBytes-total)
		key := strings.ToUpper(name)
		if _, mandatory := required[key]; mandatory {
			if err != nil {
				return nil, nil, 0, err
			}
			required[key] = true
		}
		switch {
		case errors.Is(err, ptb.ErrTooLarge):
			return nil, nil, 0, err
		case errors.Is(err, ptb.ErrMasked):
			skipped[name] = "encrypted (masked) pages are not supported"
			continue
		case errors.Is(err, ptb.ErrFormat):
			skipped[name] = "not a Btrieve data file"
			continue
		case err != nil:
			skipped[name] = "corrupt record structure"
			continue
		}
		records += len(rows)
		if records > ptbMaxRecords || recordBytes(rows) > ptbMaxTotalBytes-total {
			return nil, nil, 0, ptb.ErrTooLarge
		}
		decoded[name] = rows
		total += recordBytes(rows)
	}
	for _, found := range required {
		if !found {
			return nil, nil, 0, ptb.ErrCorrupt
		}
	}
	return decoded, skipped, total, nil
}

func recordBytes(rows [][]byte) int {
	n := 0
	for _, row := range rows {
		n += len(row)
	}
	return n
}

func sortedMapKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func recordCounts(decoded map[string][][]byte) map[string]int {
	out := make(map[string]int, len(decoded))
	for name, rows := range decoded {
		out[name] = len(rows)
	}
	return out
}

func sortedKeys(m map[string][][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// safeEntryName rejects absolute paths, parent traversal and Windows separators. The archive is
// only ever read, never extracted, but a name is still checked before it reaches a log or a key.
func safeEntryName(name string) bool {
	if name == "" || strings.ContainsAny(name, "\\:") || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "..") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}
