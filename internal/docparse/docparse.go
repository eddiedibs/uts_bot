package docparse

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/ledongthuc/pdf"
	"github.com/xuri/excelize/v2"
)

// ParsePDF extracts plain text from a PDF file's bytes.
func ParsePDF(data []byte) (string, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open pdf: %w", err)
	}
	var b strings.Builder
	for i := 1; i <= r.NumPage(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		text, err := p.GetPlainText(nil)
		if err != nil {
			continue
		}
		b.WriteString(text)
	}
	return strings.TrimSpace(b.String()), nil
}

// ParseDOCX extracts plain text from a DOCX file's bytes by reading word/document.xml.
func ParseDOCX(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", fmt.Errorf("open docx zip: %w", err)
	}
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("open document.xml: %w", err)
		}
		defer rc.Close()
		return extractXMLText(rc)
	}
	return "", fmt.Errorf("word/document.xml not found in docx")
}

// ParseXLSX extracts plain text from an XLSX file's bytes by reading all sheet rows.
func ParseXLSX(data []byte) (string, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("open xlsx: %w", err)
	}
	defer f.Close()
	var b strings.Builder
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			continue
		}
		for _, row := range rows {
			line := strings.TrimSpace(strings.Join(row, "\t"))
			if line != "" {
				b.WriteString(line)
				b.WriteByte('\n')
			}
		}
	}
	return strings.TrimSpace(b.String()), nil
}

// extractXMLText reads all CharData tokens from an XML stream and joins them as plain text.
func extractXMLText(r io.Reader) (string, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	var b strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return b.String(), nil
		}
		if cd, ok := tok.(xml.CharData); ok {
			text := strings.TrimSpace(string(cd))
			if text != "" {
				b.WriteString(text)
				b.WriteByte(' ')
			}
		}
	}
	return strings.TrimSpace(b.String()), nil
}
