package espflasher

import (
	"testing"
)

// connectStatusEvent captures a single ConnectStatus callback invocation.
type connectStatusEvent struct {
	phase       ConnectPhase
	attempt     int
	maxAttempts int
	message     string
}

// newConnectTestFlasher builds a Flasher wired to a mock port/connection,
// with no hardware reset (ResetNoReset) so the test runs fast and
// deterministically.
func newConnectTestFlasher(mc *mockConnection, connectAttempts int, status ConnectStatusFunc) *Flasher {
	return &Flasher{
		port: &mockPort{},
		conn: mc,
		opts: &FlasherOptions{
			ChipType:        ChipAuto,
			ResetMode:       ResetNoReset,
			ConnectAttempts: connectAttempts,
			ConnectStatus:   status,
		},
	}
}

// esp32DetectMockConnection returns a mockConnection that syncs immediately
// and detects as ESP32 via the legacy magic-value path (12-byte securityInfo
// response with no ChipID falls through to the magic register read).
func esp32DetectMockConnection() *mockConnection {
	secInfo := make([]byte, 12)
	return &mockConnection{
		syncFunc: func() (uint32, error) { return 0, nil },
		securityInfoFunc: func() ([]byte, error) {
			return secInfo, nil
		},
		readRegFunc: func(addr uint32) (uint32, error) {
			return 0x00F01D83, nil // ESP32 magic
		},
	}
}

// TestConnectStatusSuccessfulConnect drives a successful connect() through
// the mock connection and asserts the ConnectStatus callback observes a
// legible phase timeline: at least one reset/sync event with sane
// attempt/maxAttempts, and a detect_chip event.
func TestConnectStatusSuccessfulConnect(t *testing.T) {
	var events []connectStatusEvent
	mc := esp32DetectMockConnection()
	f := newConnectTestFlasher(mc, 7, func(phase ConnectPhase, attempt, maxAttempts int, message string) {
		events = append(events, connectStatusEvent{phase, attempt, maxAttempts, message})
	})

	if err := f.connect(); err != nil {
		t.Fatalf("connect() error: %v", err)
	}

	var sawReset, sawDetectChip bool
	for _, e := range events {
		switch e.phase {
		case ConnectPhaseReset, ConnectPhaseSync:
			sawReset = true
			if e.maxAttempts != 7 {
				t.Errorf("event %+v: maxAttempts = %d, want 7", e, e.maxAttempts)
			}
			if e.attempt < 1 || e.attempt > e.maxAttempts {
				t.Errorf("event %+v: attempt out of range", e)
			}
			if e.message == "" {
				t.Errorf("event %+v: message should be non-empty", e)
			}
		case ConnectPhaseDetectChip:
			sawDetectChip = true
		}
	}
	if !sawReset {
		t.Error("expected at least one reset/sync status event")
	}
	if !sawDetectChip {
		t.Error("expected a detect_chip status event")
	}
}

// TestConnectStatusNilCallbackNoBehaviorChange verifies the zero-overhead
// guarantee: with ConnectStatus nil, connect() runs to the same successful
// outcome, unaffected by the guarded emission points.
func TestConnectStatusNilCallbackNoBehaviorChange(t *testing.T) {
	mc := esp32DetectMockConnection()
	f := newConnectTestFlasher(mc, 7, nil)

	if err := f.connect(); err != nil {
		t.Fatalf("connect() error: %v", err)
	}
	if f.chip == nil || f.chip.ChipType != ChipESP32 {
		t.Fatalf("connect() did not detect ESP32: %+v", f.chip)
	}
}

// Note: a test asserting the attempt counter increments across a
// failed-then-succeeding retry sequence was considered but is infeasible
// with the existing mock infra. When every sync try in an attempt fails,
// connect() calls reopenPort(), which replaces f.conn with a real
// newConn(port) (bypassing mockConnection entirely) and retries opening the
// port on a hardcoded real-time 3-second deadline. Reproducing a clean,
// fast, deterministic cross-attempt retry would require either accepting
// multi-second real-clock delays per failed attempt or building a
// byte-level SLIP protocol mock port — disproportionate for this test.
