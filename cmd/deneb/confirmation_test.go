package main

import (
	"github.com/egoist/mygo/ui"
	"testing"
	"time"
)

// Legacy behavior regressions now explicitly approve the new safety step.
// Tests for cancellation and pre-confirmation state never use this helper.
func acceptConfirmation(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	tt.Frame()
	tt.Advance(200 * time.Millisecond)
	if err := tt.Click("确认" + label); err != nil {
		t.Fatalf("expected confirmation %s: %v; %v", label, err, tt.Texts())
	}
	tt.Frame()
}
