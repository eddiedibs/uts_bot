package docparse

import (
	_ "embed"
	"testing"
)

//go:embed testdata/minimal.pdf
var minimalPDF []byte

func TestParsePDF_doesNotPanic_onInvalidInput(t *testing.T) {
	t.Parallel()
	_, err := ParsePDF([]byte("%PDF-1.4\n"))
	if err == nil {
		t.Fatal("expected error for truncated pdf")
	}
}

func TestParsePDF_minimal(t *testing.T) {
	t.Parallel()
	text, err := ParsePDF(minimalPDF)
	if err != nil {
		t.Fatalf("ParsePDF: %v", err)
	}
	if text == "" {
		t.Fatal("expected non-empty text from minimal pdf")
	}
}
