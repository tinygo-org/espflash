package espflasher

import (
	"errors"
	"testing"
)

func TestStubForKnownChips(t *testing.T) {
	chips := []ChipType{
		ChipESP8266, ChipESP32, ChipESP32S2, ChipESP32S3,
		ChipESP32C2, ChipESP32C3, ChipESP32C5, ChipESP32C6, ChipESP32H2,
	}
	for _, ct := range chips {
		t.Run(ct.String(), func(t *testing.T) {
			s, ok := stubFor(ct)
			if !ok {
				t.Fatalf("stubFor(%s) returned false", ct)
			}
			if s == nil {
				t.Fatal("stubFor returned nil stub")
			}
			if len(s.text) == 0 {
				t.Error("stub text segment is empty")
			}
			if s.textStart == 0 {
				t.Error("stub text_start is zero")
			}
			if s.entry == 0 {
				t.Error("stub entry point is zero")
			}
		})
	}
}

func TestStubForUnknownChip(t *testing.T) {
	s, ok := stubFor(ChipAuto)
	if ok {
		t.Error("stubFor(ChipAuto) should return false")
	}
	if s != nil {
		t.Error("stubFor(ChipAuto) should return nil stub")
	}
}

func TestStubDataSegment(t *testing.T) {
	// Chips with a non-empty data segment in their stub.
	// All current stubs have a data segment; verify the field is populated.
	s, ok := stubFor(ChipESP32)
	if !ok {
		t.Fatal("no stub for ESP32")
	}
	// data segment may be nil or non-nil; just confirm dataStart is set when data present.
	if len(s.data) > 0 && s.dataStart == 0 {
		t.Error("stub has data bytes but dataStart is zero")
	}
}

func TestLoadStubCallsFunc(t *testing.T) {
	var called bool
	var gotEntry uint32

	s, ok := stubFor(ChipESP32)
	if !ok {
		t.Fatal("no stub for ESP32")
	}

	mc := &mockConnection{
		loadStubFunc: func(received *stub) error {
			called = true
			gotEntry = received.entry
			return nil
		},
	}

	if err := mc.loadStub(s); err != nil {
		t.Fatalf("loadStub returned error: %v", err)
	}
	if !called {
		t.Error("loadStubFunc was not called")
	}
	if gotEntry != s.entry {
		t.Errorf("loadStub passed entry %d, want %d", gotEntry, s.entry)
	}
}

func TestEraseFlashRequiresStub(t *testing.T) {
	// EraseFlash must fail when the stub is not running.
	mc := &mockConnection{stubMode: false}
	f := &Flasher{conn: mc, opts: DefaultOptions()}

	err := f.EraseFlash()
	if err == nil {
		t.Error("EraseFlash should fail without stub")
	}
	var unsupported *UnsupportedCommandError
	if !errors.As(err, &unsupported) {
		t.Errorf("expected UnsupportedCommandError, got %T: %v", err, err)
	}
}

func TestEraseFlashSucceedsWithStub(t *testing.T) {
	var called bool
	mc := &mockConnection{
		stubMode: true,
		eraseFlashFunc: func() error {
			called = true
			return nil
		},
	}
	f := &Flasher{conn: mc, opts: DefaultOptions()}

	if err := f.EraseFlash(); err != nil {
		t.Fatalf("EraseFlash returned error: %v", err)
	}
	if !called {
		t.Error("eraseFlashFunc was not called")
	}
}
