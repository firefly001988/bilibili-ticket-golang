package biliutils

import "testing"

func TestResolvePayMoneyUsesConfirmedOrderTotal(t *testing.T) {
	if got := resolvePayMoney(100, 3, 330); got != 330 {
		t.Fatalf("resolvePayMoney() = %d, want confirmed total 330", got)
	}
}

func TestResolvePayMoneyFallsBackToUnitPriceTimesCount(t *testing.T) {
	if got := resolvePayMoney(100, 3, 0); got != 300 {
		t.Fatalf("resolvePayMoney() = %d, want fallback total 300", got)
	}
}
