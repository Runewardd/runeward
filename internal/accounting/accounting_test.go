package accounting

import (
	"sync"
	"testing"
)

func TestRecordAccumulates(t *testing.T) {
	tr := New()
	tr.Record("prof-a", "sbx-1", 100, 0.50)
	tr.Record("prof-a", "sbx-1", 50, 0.25)
	tr.Record("prof-a", "sbx-2", 10, 0.10)

	if got := tr.Usage("sbx-1"); got.Tokens != 150 || got.CostUSD != 0.75 {
		t.Fatalf("sbx-1 usage = %+v, want {150 0.75}", got)
	}
	if got := tr.Usage("sbx-2"); got.Tokens != 10 || got.CostUSD != 0.10 {
		t.Fatalf("sbx-2 usage = %+v, want {10 0.10}", got)
	}
	if got := tr.ProfileUsage("prof-a"); got.Tokens != 160 || got.CostUSD != 0.85 {
		t.Fatalf("prof-a usage = %+v, want {160 0.85}", got)
	}
}

func TestUsageUnknownIsZero(t *testing.T) {
	tr := New()
	if got := tr.Usage("nope"); got != (Usage{}) {
		t.Fatalf("unknown sandbox usage = %+v, want zero", got)
	}
	if got := tr.ProfileUsage("nope"); got != (Usage{}) {
		t.Fatalf("unknown profile usage = %+v, want zero", got)
	}
}

func TestOverTokenLimit(t *testing.T) {
	tr := New()
	tr.Record("p", "s", 90, 0)

	if over, _ := tr.Over("s", 100, 0); over {
		t.Fatalf("under token limit should be false")
	}

	tr.Record("p", "s", 10, 0) // now 100, meets limit
	over, msg := tr.Over("s", 100, 0)
	if !over || msg == "" {
		t.Fatalf("at token limit should be over with message, got over=%v msg=%q", over, msg)
	}

	tr.Record("p", "s", 5, 0) // 105, exceeds
	if over, _ := tr.Over("s", 100, 0); !over {
		t.Fatalf("over token limit should be true")
	}
}

func TestOverCostLimit(t *testing.T) {
	tr := New()
	tr.Record("p", "s", 0, 4.99)
	if over, _ := tr.Over("s", 0, 5.0); over {
		t.Fatalf("under cost limit should be false")
	}

	tr.Record("p", "s", 0, 0.01) // 5.00, meets limit
	over, msg := tr.Over("s", 0, 5.0)
	if !over || msg == "" {
		t.Fatalf("at cost limit should be over with message, got over=%v msg=%q", over, msg)
	}
}

func TestOverZeroAndNegativeLimitsUnlimited(t *testing.T) {
	tr := New()
	tr.Record("p", "s", 1_000_000, 1_000_000)

	if over, _ := tr.Over("s", 0, 0); over {
		t.Fatalf("zero limits should mean unlimited")
	}
	if over, _ := tr.Over("s", -1, -1); over {
		t.Fatalf("negative limits should mean unlimited")
	}
}

func TestNegativeInputsClamped(t *testing.T) {
	tr := New()
	tr.Record("p", "s", -100, -5.0)
	if got := tr.Usage("s"); got != (Usage{}) {
		t.Fatalf("negative inputs should clamp to zero, got %+v", got)
	}

	tr.Record("p", "s", 50, 1.0)
	tr.Record("p", "s", -10, -0.5) // ignored
	if got := tr.Usage("s"); got.Tokens != 50 || got.CostUSD != 1.0 {
		t.Fatalf("usage = %+v, want {50 1.0}", got)
	}
}

func TestForgetKeepsProfileTotals(t *testing.T) {
	tr := New()
	tr.Record("prof", "sbx", 100, 2.0)

	tr.Forget("sbx")

	if got := tr.Usage("sbx"); got != (Usage{}) {
		t.Fatalf("forgotten sandbox usage = %+v, want zero", got)
	}
	if got := tr.ProfileUsage("prof"); got.Tokens != 100 || got.CostUSD != 2.0 {
		t.Fatalf("profile totals should be retained, got %+v", got)
	}
}

func TestConcurrentRecord(t *testing.T) {
	tr := New()
	const goroutines = 50
	const perGoroutine = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < perGoroutine; j++ {
				tr.Record("prof", "sbx", 1, 0.01)
			}
		}()
	}
	wg.Wait()

	wantTokens := int64(goroutines * perGoroutine)
	if got := tr.Usage("sbx"); got.Tokens != wantTokens {
		t.Fatalf("concurrent tokens = %d, want %d", got.Tokens, wantTokens)
	}
}
