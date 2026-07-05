package espflasher

import (
	"errors"
	"runtime"
	"testing"
	"time"
)

func TestTickEraseProgress(t *testing.T) {
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

	if err := tickErase(est, interval, progress, work); err != nil {
		t.Fatalf("tickErase returned error: %v", err)
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

func TestTickEraseErrorOmitsFinalCall(t *testing.T) {
	interval := 2 * time.Millisecond
	est := 50 * time.Millisecond
	wantErr := errors.New("erase failed")

	var calls [][2]int
	progress := func(current, total int) {
		calls = append(calls, [2]int{current, total})
	}
	work := func() error {
		time.Sleep(4 * interval)
		return wantErr
	}

	err := tickErase(est, interval, progress, work)
	if !errors.Is(err, wantErr) {
		t.Fatalf("tickErase error = %v, want %v", err, wantErr)
	}

	estMs := int(est / time.Millisecond)
	for i, c := range calls {
		if c[0] == estMs && c[1] == estMs {
			t.Errorf("call %d reported completion (%d, %d) despite work returning an error", i, c[0], c[1])
		}
	}
}

func TestTickEraseJoinsGoroutine(t *testing.T) {
	before := runtime.NumGoroutine()

	interval := time.Millisecond
	est := 20 * time.Millisecond
	work := func() error {
		time.Sleep(5 * interval)
		return nil
	}

	if err := tickErase(est, interval, func(int, int) {}, work); err != nil {
		t.Fatalf("tickErase returned error: %v", err)
	}

	// The ticker goroutine is joined synchronously inside tickErase, so the
	// count should already be back to baseline. Poll briefly anyway to absorb
	// unrelated runtime bookkeeping goroutines.
	deadline := time.Now().Add(200 * time.Millisecond)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := runtime.NumGoroutine(); got > before {
		t.Errorf("goroutine count after tickErase = %d, want <= %d (possible leak)", got, before)
	}
}

func TestTickErasePanicJoinsGoroutineAndPropagates(t *testing.T) {
	before := runtime.NumGoroutine()

	interval := time.Millisecond
	est := 20 * time.Millisecond
	wantPanic := "erase blew up"
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
				t.Fatal("expected tickErase to panic, but it did not")
			}
			if r != wantPanic {
				t.Errorf("recovered panic = %v, want %v", r, wantPanic)
			}
		}()
		_ = tickErase(est, interval, progress, work)
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
		t.Errorf("goroutine count after tickErase panic = %d, want <= %d (possible leak)", got, before)
	}
}

func TestEraseFlashProgressWiring(t *testing.T) {
	var calls [][2]int
	mc := &mockConnection{
		stubMode: true,
		eraseFlashFunc: func() error {
			return nil
		},
	}
	f := &Flasher{conn: mc, opts: DefaultOptions()}

	err := f.EraseFlash(func(current, total int) {
		calls = append(calls, [2]int{current, total})
	})
	if err != nil {
		t.Fatalf("EraseFlash returned error: %v", err)
	}
	if len(calls) == 0 {
		t.Fatal("expected at least the final progress call")
	}

	wantMs := int(chipEraseTimeout / time.Millisecond)
	last := calls[len(calls)-1]
	if last[0] != wantMs || last[1] != wantMs {
		t.Errorf("final progress = %v, want (%d, %d)", last, wantMs, wantMs)
	}
}

func TestEraseRegionProgressWiring(t *testing.T) {
	var calls [][2]int
	mc := &mockConnection{
		stubMode: true,
		eraseRegionFunc: func(offset, size uint32) error {
			return nil
		},
	}
	f := &Flasher{conn: mc, opts: DefaultOptions()}

	size := uint32(0x10000)
	err := f.EraseRegion(0x10000, size, func(current, total int) {
		calls = append(calls, [2]int{current, total})
	})
	if err != nil {
		t.Fatalf("EraseRegion returned error: %v", err)
	}
	if len(calls) == 0 {
		t.Fatal("expected at least the final progress call")
	}

	wantMs := int(eraseTimeoutForSize(size) / time.Millisecond)
	last := calls[len(calls)-1]
	if last[0] != wantMs || last[1] != wantMs {
		t.Errorf("final progress = %v, want (%d, %d)", last, wantMs, wantMs)
	}
}
