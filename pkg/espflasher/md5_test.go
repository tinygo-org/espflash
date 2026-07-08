package espflasher

import (
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestTickMD5Progress(t *testing.T) {
	interval := 2 * time.Millisecond
	est := 50 * time.Millisecond

	var calls [][2]int
	progress := func(current, total int) {
		calls = append(calls, [2]int{current, total})
	}
	work := func() error {
		time.Sleep(6 * interval)
		return nil
	}

	if err := tickMD5(est, interval, progress, work); err != nil {
		t.Fatalf("tickMD5 returned error: %v", err)
	}

	if len(calls) < 2 {
		t.Fatalf("expected at least one intermediate tick plus the final call, got %d calls", len(calls))
	}

	estMs := int(est / time.Millisecond)
	last := calls[len(calls)-1]
	if last[0] != estMs || last[1] != estMs {
		t.Errorf("final call = %v, want (%d, %d)", last, estMs, estMs)
	}

	prev := -1
	for i, c := range calls[:len(calls)-1] {
		if c[1] != estMs {
			t.Errorf("call %d total = %d, want %d", i, c[1], estMs)
		}
		if c[0] >= estMs {
			t.Errorf("intermediate call %d current = %d, must be < total %d", i, c[0], estMs)
		}
		if c[0] < prev {
			t.Errorf("call %d current = %d is not monotonically non-decreasing (prev %d)", i, c[0], prev)
		}
		prev = c[0]
	}
}

func TestTickMD5ErrorOmitsFinalCall(t *testing.T) {
	interval := 2 * time.Millisecond
	est := 50 * time.Millisecond
	wantErr := errors.New("md5 failed")

	var calls [][2]int
	progress := func(current, total int) {
		calls = append(calls, [2]int{current, total})
	}
	work := func() error {
		time.Sleep(4 * interval)
		return wantErr
	}

	err := tickMD5(est, interval, progress, work)
	if !errors.Is(err, wantErr) {
		t.Fatalf("tickMD5 error = %v, want %v", err, wantErr)
	}

	estMs := int(est / time.Millisecond)
	for i, c := range calls {
		if c[0] == estMs && c[1] == estMs {
			t.Errorf("call %d reported completion (%d, %d) despite work returning an error", i, c[0], c[1])
		}
	}
}

func TestTickMD5JoinsGoroutine(t *testing.T) {
	before := runtime.NumGoroutine()

	interval := time.Millisecond
	est := 20 * time.Millisecond
	work := func() error {
		time.Sleep(5 * interval)
		return nil
	}

	if err := tickMD5(est, interval, func(int, int) {}, work); err != nil {
		t.Fatalf("tickMD5 returned error: %v", err)
	}

	// The ticker goroutine is joined synchronously inside tickMD5, so the
	// count should already be back to baseline. Poll briefly anyway to absorb
	// unrelated runtime bookkeeping goroutines.
	deadline := time.Now().Add(200 * time.Millisecond)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before {
		t.Errorf("goroutine count after tickMD5 = %d, want <= %d (possible leak)", got, before)
	}
}

func TestTickMD5PanicJoinsGoroutineAndPropagates(t *testing.T) {
	before := runtime.NumGoroutine()

	interval := time.Millisecond
	est := 20 * time.Millisecond
	wantPanic := "md5 blew up"
	work := func() error {
		time.Sleep(5 * interval)
		panic(wantPanic)
	}

	var calls [][2]int
	progress := func(current, total int) {
		calls = append(calls, [2]int{current, total})
	}

	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected tickMD5 to panic, but it did not")
			}
			if r != wantPanic {
				t.Errorf("recovered panic = %v, want %v", r, wantPanic)
			}
		}()
		_ = tickMD5(est, interval, progress, work)
	}()

	estMs := int(est / time.Millisecond)
	for i, c := range calls {
		if c[0] == estMs && c[1] == estMs {
			t.Errorf("call %d reported completion (%d, %d) despite work panicking", i, c[0], c[1])
		}
	}

	// The ticker goroutine must be joined during panic unwinding, so the
	// count should already be back to baseline. Poll briefly anyway to absorb
	// unrelated runtime bookkeeping goroutines.
	deadline := time.Now().Add(200 * time.Millisecond)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before {
		t.Errorf("goroutine count after tickMD5 panic = %d, want <= %d (possible leak)", got, before)
	}
}

func TestTickMD5IntermediatePanicDropsTickAndContinues(t *testing.T) {
	before := runtime.NumGoroutine()

	interval := time.Millisecond
	est := 50 * time.Millisecond

	// Panic only on an intermediate tick (not the first, not the final).
	// The first intermediate call is allowed through, the second panics,
	// and the final completion call must still be delivered normally.
	var calls [][2]int
	intermediate := 0
	estMs := int(est / time.Millisecond)
	progress := func(current, total int) {
		if current != estMs { // intermediate tick
			intermediate++
			if intermediate == 2 {
				panic("progress callback blew up on an intermediate tick")
			}
		}
		calls = append(calls, [2]int{current, total})
	}
	work := func() error {
		time.Sleep(10 * interval)
		return nil
	}

	// A panic on an intermediate tick must not crash the process; tickMD5
	// must still return normally.
	if err := tickMD5(est, interval, progress, work); err != nil {
		t.Fatalf("tickMD5 returned error: %v", err)
	}

	if len(calls) == 0 {
		t.Fatal("expected at least the final progress call")
	}
	last := calls[len(calls)-1]
	if last[0] != estMs || last[1] != estMs {
		t.Errorf("final call = %v, want (%d, %d) — final tick must still fire after a dropped intermediate tick", last, estMs, estMs)
	}

	// The ticker goroutine must still be joined after a dropped tick.
	deadline := time.Now().Add(200 * time.Millisecond)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before {
		t.Errorf("goroutine count after intermediate-panic tickMD5 = %d, want <= %d (possible leak)", got, before)
	}
}

func TestGetFlashMD5NilProgressUnchanged(t *testing.T) {
	before := runtime.NumGoroutine()

	mc := &mockConnection{
		stubMode: true,
		flashMD5Func: func(addr, size uint32) ([]byte, error) {
			return []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
				0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10}, nil
		},
	}
	f := &Flasher{conn: mc, opts: DefaultOptions(), chip: chipDefs[ChipESP8266]}

	got, err := f.GetFlashMD5(0, 1024, nil)
	if err != nil {
		t.Fatalf("GetFlashMD5 returned error: %v", err)
	}
	want := "0102030405060708090a0b0c0d0e0f10"
	if got != want {
		t.Errorf("GetFlashMD5 = %q, want %q", got, want)
	}

	deadline := time.Now().Add(200 * time.Millisecond)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if gotN := runtime.NumGoroutine(); gotN > before {
		t.Errorf("goroutine count after nil-progress GetFlashMD5 = %d, want <= %d (no ticker should run)", gotN, before)
	}
}

func TestGetFlashMD5ProgressWiring(t *testing.T) {
	var calls [][2]int
	mc := &mockConnection{
		stubMode: true,
		flashMD5Func: func(addr, size uint32) ([]byte, error) {
			return make([]byte, 16), nil
		},
	}
	f := &Flasher{conn: mc, opts: DefaultOptions(), chip: chipDefs[ChipESP8266]}

	size := uint32(512 * 1024)
	_, err := f.GetFlashMD5(0, size, func(current, total int) {
		calls = append(calls, [2]int{current, total})
	})
	if err != nil {
		t.Fatalf("GetFlashMD5 returned error: %v", err)
	}
	if len(calls) == 0 {
		t.Fatal("expected at least the final progress call")
	}

	wantMs := int(md5TimeoutForSize(size) / time.Millisecond)
	last := calls[len(calls)-1]
	if last[0] != wantMs || last[1] != wantMs {
		t.Errorf("final progress = %v, want (%d, %d)", last, wantMs, wantMs)
	}
}
