package pagos_test

import (
	"context"
	"os"
	"testing"
	"time"

	"uts_bot/internal/pagos"
)

func TestGetPaymentFees_Live(t *testing.T) {
	ci := os.Getenv("PAGOS_CI")
	pass := os.Getenv("PAGOS_PASSWORD")
	if ci == "" || pass == "" {
		t.Skip("PAGOS_CI / PAGOS_PASSWORD not set")
	}
	base := os.Getenv("PAGOS_API_BASE_URL")
	if base == "" {
		base = "https://uftapp.uft.edu.ve/"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cl := pagos.New(base, ci, pass)
	fees, err := cl.GetPaymentFees(ctx)
	if err != nil {
		t.Fatalf("GetPaymentFees: %v", err)
	}
	if fees == nil {
		t.Fatal("nil fees")
	}
	if len(fees.Cuotas) == 0 {
		t.Fatal("expected at least one cuota")
	}
	c0 := fees.Cuotas[0]
	if c0.ID == 0 || c0.Nombre == "" || c0.Monto == "" {
		t.Fatalf("incomplete cuota: id=%d nombre=%q monto=%q", c0.ID, c0.Nombre, c0.Monto)
	}
	t.Logf("ok: %d cuotas (first id=%d estado=%s)", len(fees.Cuotas), c0.ID, c0.Estado)
}
