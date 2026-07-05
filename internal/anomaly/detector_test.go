package anomaly

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Runewardd/runeward/internal/ledger"
)

// newTestDetector returns a detector with a silenced logger and a controllable
// clock anchored at a fixed base time.
func newTestDetector(t *testing.T) (*Detector, *fakeClock) {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	d := New(logger)
	clk := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	d.now = clk.Now
	return d, clk
}

// fakeClock is a deterministic clock for driving sliding windows and rate
// limiting.
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

func shellEvent(session string, when time.Time) ledger.Event {
	return ledger.Event{
		SessionID: session,
		Tool:      "shell",
		Action:    "echo hi",
		Verdict:   "allow",
		Time:      when,
	}
}

func netEvent(session, action string, when time.Time) ledger.Event {
	return ledger.Event{
		SessionID: session,
		Tool:      "net",
		Action:    action,
		Verdict:   "allow",
		Time:      when,
	}
}

func denyEvent(session string, when time.Time) ledger.Event {
	return ledger.Event{
		SessionID: session,
		Tool:      "shell",
		Action:    "rm -rf /",
		Verdict:   "deny",
		Time:      when,
	}
}

func TestExecBurstWithinWindow(t *testing.T) {
	d, _ := newTestDetector(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Feed one more than the burst threshold, all inside a single window.
	total := d.execBurst + 5
	for i := 0; i < total; i++ {
		d.Emit(shellEvent("s1", base.Add(time.Duration(i)*time.Millisecond)))
	}

	if got := d.Counts().ExecBurst; got != 1 {
		t.Fatalf("ExecBurst = %d, want exactly 1 (rate limited)", got)
	}
}

func TestExecBurstSpacedBeyondWindowDoesNotFire(t *testing.T) {
	d, _ := newTestDetector(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Space execs two windows apart so at most one is ever in the window.
	total := d.execBurst + 5
	for i := 0; i < total; i++ {
		when := base.Add(time.Duration(i) * 2 * d.window)
		d.Emit(shellEvent("s1", when))
	}

	if got := d.Counts().ExecBurst; got != 0 {
		t.Fatalf("ExecBurst = %d, want 0 (events outside window)", got)
	}
}

func TestNovelHostThreshold(t *testing.T) {
	d, _ := newTestDetector(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Contact one more than the host threshold of distinct destinations.
	for i := 0; i <= d.maxHosts; i++ {
		action := "https://host" + itoa(i) + ".example.com:443/path"
		d.Emit(netEvent("s1", action, base.Add(time.Duration(i)*time.Second)))
	}

	if got := d.Counts().NovelHost; got != 1 {
		t.Fatalf("NovelHost = %d, want exactly 1", got)
	}
}

func TestNovelHostRepeatedSameHostDoesNotFire(t *testing.T) {
	d, _ := newTestDetector(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// The same host many times is a single distinct destination.
	for i := 0; i < d.maxHosts+50; i++ {
		d.Emit(netEvent("s1", "https://only.example.com/x", base.Add(time.Duration(i)*time.Second)))
	}

	if got := d.Counts().NovelHost; got != 0 {
		t.Fatalf("NovelHost = %d, want 0 (single distinct host)", got)
	}
}

func TestDenialSpikeThreshold(t *testing.T) {
	d, _ := newTestDetector(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i <= d.maxDenies; i++ {
		d.Emit(denyEvent("s1", base.Add(time.Duration(i)*time.Millisecond)))
	}

	if got := d.Counts().DenialSpike; got != 1 {
		t.Fatalf("DenialSpike = %d, want exactly 1", got)
	}
}

func TestSessionsTrackedIndependently(t *testing.T) {
	d, _ := newTestDetector(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// s1 bursts; s2 stays well under the threshold.
	for i := 0; i < d.execBurst+5; i++ {
		d.Emit(shellEvent("s1", base.Add(time.Duration(i)*time.Millisecond)))
	}
	for i := 0; i < d.execBurst/2; i++ {
		d.Emit(shellEvent("s2", base.Add(time.Duration(i)*time.Millisecond)))
	}

	if got := d.Counts().ExecBurst; got != 1 {
		t.Fatalf("ExecBurst = %d, want exactly 1 (only s1 should trip)", got)
	}
}

func TestRateLimitRecoversAfterCooldown(t *testing.T) {
	d, _ := newTestDetector(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// First burst.
	for i := 0; i < d.execBurst+2; i++ {
		d.Emit(shellEvent("s1", base.Add(time.Duration(i)*time.Millisecond)))
	}
	if got := d.Counts().ExecBurst; got != 1 {
		t.Fatalf("after first burst ExecBurst = %d, want 1", got)
	}

	// A second burst more than a cooldown later should fire again. Keep the
	// events inside a fresh window that starts after the cooldown.
	second := base.Add(2 * warnCooldown)
	for i := 0; i < d.execBurst+2; i++ {
		d.Emit(shellEvent("s1", second.Add(time.Duration(i)*time.Millisecond)))
	}
	if got := d.Counts().ExecBurst; got != 2 {
		t.Fatalf("after second burst ExecBurst = %d, want 2", got)
	}
}

func TestParseHost(t *testing.T) {
	cases := map[string]string{
		"https://example.com/path":  "example.com",
		"example.com:443":           "example.com",
		"http://user:pass@Foo.com/": "foo.com",
		"bare.host":                 "bare.host",
		"[::1]:8080":                "::1",
		"":                          "",
	}
	for in, want := range cases {
		if got := parseHost(in); got != want {
			t.Errorf("parseHost(%q) = %q, want %q", in, got, want)
		}
	}
}

// itoa is a tiny helper to avoid importing strconv in the test for a single
// use; it handles the small non-negative indices used here.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}
