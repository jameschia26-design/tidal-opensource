package main

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"strings"
	"unicode/utf8"
)

// pdfDoc is a minimal single-font PDF writer (Helvetica, WinAnsi text) using only the standard library.
// Text is limited to printable ASCII-compatible runes; other runes are replaced with "?".
// ponytail: a full Unicode font (Noto Sans CJK/Tamil) is multi-megabyte and needs a CID font + ToUnicode
// CMap; the ceiling is disclosed on /limits. Upgrade only when customers need non-Latin documents.
type pdfDoc struct {
	pages []string
	cur   *strings.Builder
	y     float64
	// logo is drawn once, in the top-right corner of the first page.
	logo *pdfImage
}

// pdfImage is a decoded, downscaled logo kept as raw RGB so the PDF needs no external image library.
type pdfImage struct {
	W, H int
	RGB  []byte
}

const (
	pdfLogoMaxW = 150.0
	pdfLogoMaxH = 50.0
)

const (
	pdfW, pdfH = 595.0, 842.0 // A4 in points
	pdfMargin  = 50.0
)

func newPDF() *pdfDoc {
	d := &pdfDoc{}
	d.newPage()
	return d
}

// decodeLogo turns PNG or JPEG bytes into raw RGB, scaled down to fit the header without distortion.
func decodeLogo(b []byte) (*pdfImage, error) {
	// Read the header first: an oversized image is refused before its pixels are decoded (R19).
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(b)); err != nil {
		return nil, err
	} else if cfg.Width > logoMaxPixels || cfg.Height > logoMaxPixels || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("logo dimensions %dx%d exceed %d", cfg.Width, cfg.Height, logoMaxPixels)
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("empty image")
	}
	// Keep the pixels small: a document only ever prints the logo a few centimetres wide.
	const maxPx = 300
	if w > maxPx {
		h = h * maxPx / w
		w = maxPx
	}
	if h > 120 {
		w = w * 120 / h
		h = 120
	}
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	out := make([]byte, 0, w*h*3)
	for y := 0; y < h; y++ {
		sy := bounds.Min.Y + y*bounds.Dy()/h
		for x := 0; x < w; x++ {
			sx := bounds.Min.X + x*bounds.Dx()/w
			r, g, bl, _ := img.At(sx, sy).RGBA()
			out = append(out, byte(r>>8), byte(g>>8), byte(bl>>8))
		}
	}
	return &pdfImage{W: w, H: h, RGB: out}, nil
}

// logoBox returns the drawn width and height in points, keeping the image's proportions.
func (p *pdfImage) logoBox() (float64, float64) {
	w, h := pdfLogoMaxW, pdfLogoMaxW*float64(p.H)/float64(p.W)
	if h > pdfLogoMaxH {
		h = pdfLogoMaxH
		w = pdfLogoMaxH * float64(p.W) / float64(p.H)
	}
	return w, h
}

func (d *pdfDoc) newPage() {
	if d.cur != nil {
		d.pages = append(d.pages, d.cur.String())
	}
	d.cur = &strings.Builder{}
	d.y = pdfH - pdfMargin
}

// pdfSafe keeps printable ASCII and maps everything else to "?" so the content stream stays valid.
func pdfSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == utf8.RuneError || r < 32 || r > 126 {
			b.WriteByte('?')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func pdfEscape(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "(", `\(`)
	return strings.ReplaceAll(s, ")", `\)`)
}

func (d *pdfDoc) text(x float64, size float64, bold bool, s string) {
	font := "F1"
	if bold {
		font = "F2"
	}
	fmt.Fprintf(d.cur, "BT /%s %.1f Tf %.1f %.1f Td (%s) Tj ET\n", font, size, x, d.y, pdfEscape(pdfSafe(s)))
}

// line writes one row of text at the current y position and advances it. It starts a new page when needed.
func (d *pdfDoc) line(size float64, bold bool, s string) {
	if d.y < pdfMargin+size {
		d.newPage()
	}
	d.text(pdfMargin, size, bold, s)
	d.y -= size * 1.5
}

// rightText writes right-aligned text on the current line without advancing.
func (d *pdfDoc) rightText(right float64, size float64, bold bool, s string) {
	x := right - float64(len(pdfSafe(s)))*size*0.5
	d.text(x, size, bold, s)
}

func (d *pdfDoc) gap(h float64) {
	d.y -= h
	if d.y < pdfMargin {
		d.newPage()
	}
}

func (d *pdfDoc) Bytes() []byte {
	pages := append(append([]string{}, d.pages...), d.cur.String())
	// A logo adds one object before the page objects, so every later reference shifts by one.
	imgID := 0
	if d.logo != nil {
		imgID = 5
	}
	firstPage := 0
	if imgID == 0 {
		firstPage = 5
	} else {
		firstPage = 6
		if w, h := d.logo.logoBox(); w > 0 {
			draw := fmt.Sprintf("q %.1f 0 0 %.1f %.1f %.1f cm /Im1 Do Q\n", w, h, pdfW-pdfMargin-w, pdfH-pdfMargin-h)
			pages[0] = draw + pages[0]
		}
	}
	var buf bytes.Buffer
	offsets := []int{}
	write := func(s string) { buf.WriteString(s) }
	objID := func() int { return len(offsets) + 1 }
	// Object numbering: 1 catalog, 2 pages, 3 Helvetica, 4 Helvetica-Bold, then per page: page + content.
	kids := []string{}
	for i := range pages {
		kids = append(kids, fmt.Sprintf("%d 0 R", firstPage+i*2))
	}
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pages)),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>",
	}
	if imgID != 0 {
		var img bytes.Buffer
		zw := zlib.NewWriter(&img)
		_, _ = zw.Write(d.logo.RGB)
		_ = zw.Close()
		objs = append(objs, fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>\nstream\n%s\nendstream", d.logo.W, d.logo.H, img.Len(), img.String()))
	}
	xobj := ""
	if imgID != 0 {
		xobj = " /XObject << /Im1 5 0 R >>"
	}
	for i, content := range pages {
		objs = append(objs,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.0f %.0f] /Resources << /Font << /F1 3 0 R /F2 4 0 R >>%s >> /Contents %d 0 R >>", pdfW, pdfH, xobj, firstPage+1+i*2),
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content),
		)
	}
	write("%PDF-1.4\n")
	for i, o := range objs {
		offsets = append(offsets, buf.Len())
		write(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", i+1, o))
	}
	xref := buf.Len()
	write(fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", objID()))
	for _, off := range offsets {
		write(fmt.Sprintf("%010d 00000 n \n", off))
	}
	write(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", objID(), xref))
	return buf.Bytes()
}
