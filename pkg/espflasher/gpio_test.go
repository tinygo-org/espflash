package espflasher

import (
	"errors"
	"testing"
)

// regOp records a single ordered register operation performed through the
// connection interface.
type regOp struct {
	op   string // "r" (readReg) or "w" (writeReg)
	addr uint32
	val  uint32 // only meaningful for "w"
}

// newRecordingConn returns a mockConnection whose readReg always returns 0
// and whose readReg/writeReg calls are recorded in order, along with the
// backing slice.
func newRecordingConn() (*mockConnection, *[]regOp) {
	ops := &[]regOp{}
	mc := &mockConnection{}
	mc.readRegFunc = func(addr uint32) (uint32, error) {
		*ops = append(*ops, regOp{op: "r", addr: addr})
		return 0, nil
	}
	mc.writeRegFunc = func(addr, value, mask, delayUS uint32) error {
		*ops = append(*ops, regOp{op: "w", addr: addr, val: value})
		return nil
	}
	return mc, ops
}

func flasherFor(ct ChipType, mc *mockConnection) *Flasher {
	return &Flasher{conn: mc, chip: &chipDef{ChipType: ct}, opts: DefaultOptions()}
}

func assertOps(t *testing.T, got []regOp, want []regOp) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("op count = %d, want %d\ngot:  %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("op[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// --- ESP32, pin 16: PIN_FUNC_GPIO=2, IO_MUX offset 0x4c, pin <32 ---

const (
	esp32Pin16IOMux  = 0x3FF49000 + 0x4c
	esp32Pin16OutSel = 0x3FF44530 + 4*16
)

func TestSetGPIOESP32High(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32, mc)

	if err := f.SetGPIO(16, true); err != nil {
		t.Fatalf("SetGPIO: %v", err)
	}
	assertOps(t, *ops, []regOp{
		{"r", esp32Pin16IOMux, 0},
		{"w", esp32Pin16IOMux, 2 << 12},
		{"r", esp32Pin16OutSel, 0},
		{"w", esp32Pin16OutSel, 256},
		{"w", 0x3FF44008, 1 << 16}, // OUT_W1TS
		{"w", 0x3FF44024, 1 << 16}, // ENABLE_W1TS
	})
}

func TestSetGPIOESP32Low(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32, mc)

	if err := f.SetGPIO(16, false); err != nil {
		t.Fatalf("SetGPIO: %v", err)
	}
	assertOps(t, *ops, []regOp{
		{"r", esp32Pin16IOMux, 0},
		{"w", esp32Pin16IOMux, 2 << 12},
		{"r", esp32Pin16OutSel, 0},
		{"w", esp32Pin16OutSel, 256},
		{"w", 0x3FF4400C, 1 << 16}, // OUT_W1TC
		{"w", 0x3FF44024, 1 << 16}, // ENABLE_W1TS
	})
}

func TestReadGPIOESP32(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32, mc)

	v, err := f.ReadGPIO(16)
	if err != nil {
		t.Fatalf("ReadGPIO: %v", err)
	}
	if v {
		t.Error("expected level false (mock IN reads 0)")
	}
	assertOps(t, *ops, []regOp{
		{"r", esp32Pin16IOMux, 0},
		{"w", esp32Pin16IOMux, (2 << 12) | ioMuxFunIEBit},
		{"w", 0x3FF44028, 1 << 16}, // ENABLE_W1TC
		{"r", 0x3FF4403C, 0},       // IN
	})
}

func TestReleaseGPIOESP32(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32, mc)

	if err := f.ReleaseGPIO(16); err != nil {
		t.Fatalf("ReleaseGPIO: %v", err)
	}
	assertOps(t, *ops, []regOp{
		{"w", 0x3FF44028, 1 << 16}, // ENABLE_W1TC
		{"r", esp32Pin16IOMux, 0},
		{"w", esp32Pin16IOMux, ioMuxFunIEBit},
	})
}

func TestESP32GPIO2NonLinearIOMuxOffset(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32, mc)

	if _, err := f.ReadGPIO(2); err != nil {
		t.Fatalf("ReadGPIO: %v", err)
	}
	wantAddr := uint32(0x3FF49000 + 0x40)
	if (*ops)[0].addr != wantAddr {
		t.Errorf("GPIO2 IO_MUX addr = %#x, want %#x", (*ops)[0].addr, wantAddr)
	}
}

// --- ESP32, pin 32: high-word regs (OUT1/ENABLE1/IN1), PIN_FUNC_GPIO=2 ---

const (
	esp32Pin32IOMux  = 0x3FF49000 + 0x1c
	esp32Pin32OutSel = 0x3FF44530 + 4*32
)

func TestSetGPIOESP32HighWordPin(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32, mc)

	if err := f.SetGPIO(32, true); err != nil {
		t.Fatalf("SetGPIO: %v", err)
	}
	assertOps(t, *ops, []regOp{
		{"r", esp32Pin32IOMux, 0},
		{"w", esp32Pin32IOMux, 2 << 12},
		{"r", esp32Pin32OutSel, 0},
		{"w", esp32Pin32OutSel, 256},
		{"w", 0x3FF44014, 1 << 0}, // OUT1_W1TS, bit = 32-32
		{"w", 0x3FF44030, 1 << 0}, // ENABLE1_W1TS
	})
}

func TestReadGPIOESP32HighWordPin(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32, mc)

	if _, err := f.ReadGPIO(32); err != nil {
		t.Fatalf("ReadGPIO: %v", err)
	}
	assertOps(t, *ops, []regOp{
		{"r", esp32Pin32IOMux, 0},
		{"w", esp32Pin32IOMux, (2 << 12) | ioMuxFunIEBit},
		{"w", 0x3FF44034, 1 << 0}, // ENABLE1_W1TC
		{"r", 0x3FF44040, 0},      // IN1
	})
}

func TestReleaseGPIOESP32HighWordPin(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32, mc)

	if err := f.ReleaseGPIO(32); err != nil {
		t.Fatalf("ReleaseGPIO: %v", err)
	}
	assertOps(t, *ops, []regOp{
		{"w", 0x3FF44034, 1 << 0}, // ENABLE1_W1TC
		{"r", esp32Pin32IOMux, 0},
		{"w", esp32Pin32IOMux, ioMuxFunIEBit},
	})
}

// --- ESP32-S2, pin 33: high-word regs (OUT1/ENABLE1/IN1), PIN_FUNC_GPIO=1 ---

const (
	s2Pin33IOMux  = 0x3F409000 + 0x88
	s2Pin33OutSel = 0x3F404554 + 4*33
)

func TestSetGPIOS2HighWordPin(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32S2, mc)

	if err := f.SetGPIO(33, true); err != nil {
		t.Fatalf("SetGPIO: %v", err)
	}
	assertOps(t, *ops, []regOp{
		{"r", s2Pin33IOMux, 0},
		{"w", s2Pin33IOMux, 1 << 12},
		{"r", s2Pin33OutSel, 0},
		{"w", s2Pin33OutSel, 256},
		{"w", 0x3F404014, 1 << 1}, // OUT1_W1TS, bit = 33-32
		{"w", 0x3F404030, 1 << 1}, // ENABLE1_W1TS
	})
}

func TestReadGPIOS2HighWordPin(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32S2, mc)

	if _, err := f.ReadGPIO(33); err != nil {
		t.Fatalf("ReadGPIO: %v", err)
	}
	assertOps(t, *ops, []regOp{
		{"r", s2Pin33IOMux, 0},
		{"w", s2Pin33IOMux, (1 << 12) | ioMuxFunIEBit},
		{"w", 0x3F404034, 1 << 1}, // ENABLE1_W1TC, bit = 33-32
		{"r", 0x3F404040, 0},      // IN1
	})
}

func TestReleaseGPIOS2HighWordPin(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32S2, mc)

	if err := f.ReleaseGPIO(33); err != nil {
		t.Fatalf("ReleaseGPIO: %v", err)
	}
	assertOps(t, *ops, []regOp{
		{"w", 0x3F404034, 1 << 1}, // ENABLE1_W1TC
		{"r", s2Pin33IOMux, 0},
		{"w", s2Pin33IOMux, ioMuxFunIEBit},
	})
}

// --- ESP32-C3, pin 5: sentinel 128, PIN_FUNC_GPIO=1, no high-word regs ---

const (
	c3Pin5IOMux  = 0x60009000 + 0x18
	c3Pin5OutSel = 0x60004554 + 4*5
)

func TestSetGPIOC3Sentinel(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32C3, mc)

	if err := f.SetGPIO(5, true); err != nil {
		t.Fatalf("SetGPIO: %v", err)
	}
	assertOps(t, *ops, []regOp{
		{"r", c3Pin5IOMux, 0},
		{"w", c3Pin5IOMux, 1 << 12},
		{"r", c3Pin5OutSel, 0},
		{"w", c3Pin5OutSel, 128},
		{"w", 0x60004008, 1 << 5}, // OUT_W1TS
		{"w", 0x60004024, 1 << 5}, // ENABLE_W1TS
	})
}

func TestReadGPIOC3(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32C3, mc)

	if _, err := f.ReadGPIO(5); err != nil {
		t.Fatalf("ReadGPIO: %v", err)
	}
	assertOps(t, *ops, []regOp{
		{"r", c3Pin5IOMux, 0},
		{"w", c3Pin5IOMux, (1 << 12) | ioMuxFunIEBit},
		{"w", 0x60004028, 1 << 5}, // ENABLE_W1TC
		{"r", 0x6000403C, 0},      // IN
	})
}

func TestReleaseGPIOC3(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32C3, mc)

	if err := f.ReleaseGPIO(5); err != nil {
		t.Fatalf("ReleaseGPIO: %v", err)
	}
	assertOps(t, *ops, []regOp{
		{"w", 0x60004028, 1 << 5}, // ENABLE_W1TC
		{"r", c3Pin5IOMux, 0},
		{"w", c3Pin5IOMux, ioMuxFunIEBit},
	})
}

func TestC3GPIO5LinearIOMuxOffset(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32C3, mc)

	if _, err := f.ReadGPIO(5); err != nil {
		t.Fatalf("ReadGPIO: %v", err)
	}
	wantAddr := uint32(0x60009000 + 0x18)
	if (*ops)[0].addr != wantAddr {
		t.Errorf("GPIO5 IO_MUX addr = %#x, want %#x", (*ops)[0].addr, wantAddr)
	}
}

// --- Input-only pins must refuse SetGPIO ---

func TestSetGPIOInputOnlyRefused(t *testing.T) {
	cases := []struct {
		chip ChipType
		pin  int
	}{
		{ChipESP32, 34},
		{ChipESP32, 39},
		{ChipESP32S2, 46},
	}
	for _, c := range cases {
		mc, _ := newRecordingConn()
		f := flasherFor(c.chip, mc)
		if err := f.SetGPIO(c.pin, true); err == nil {
			t.Errorf("%s pin %d: expected error for input-only pin", c.chip, c.pin)
		}
	}
}

// --- Nonexistent pins error on every method ---

func TestNonexistentPinErrorsOnAllMethods(t *testing.T) {
	cases := []struct {
		chip ChipType
		pin  int
	}{
		{ChipESP32, 24},
		{ChipESP32, 20},
		{ChipESP32, 28},
		{ChipESP32S2, 22},
		{ChipESP32S2, 25},
	}
	for _, c := range cases {
		mc, _ := newRecordingConn()
		f := flasherFor(c.chip, mc)

		if err := f.SetGPIO(c.pin, true); err == nil {
			t.Errorf("%s pin %d: SetGPIO expected error", c.chip, c.pin)
		}
		if _, err := f.ReadGPIO(c.pin); err == nil {
			t.Errorf("%s pin %d: ReadGPIO expected error", c.chip, c.pin)
		}
		if err := f.ReleaseGPIO(c.pin); err == nil {
			t.Errorf("%s pin %d: ReleaseGPIO expected error", c.chip, c.pin)
		}
		reserved, reason := f.GPIOReserved(c.pin)
		if !reserved || reason != "nonexistent" {
			t.Errorf("%s pin %d: GPIOReserved = (%v, %q), want (true, \"nonexistent\")", c.chip, c.pin, reserved, reason)
		}
	}
}

// --- GPIOReserved classifications ---

func TestGPIOReservedClassifications(t *testing.T) {
	cases := []struct {
		chip ChipType
		pin  int
		want string
	}{
		{ChipESP32, 6, "flash"},
		{ChipESP32, 0, "strap"},
		{ChipESP32, 1, "uart0"},
		{ChipESP32, 34, "input-only"},
		{ChipESP32S2, 26, "flash/psram"},
		{ChipESP32S2, 45, "strap"},
		{ChipESP32S2, 43, "uart0"},
		{ChipESP32C3, 12, "flash"},
		{ChipESP32C3, 2, "strap"},
		{ChipESP32C3, 18, "usb-jtag"},
		{ChipESP32C3, 20, "uart0"},
		{ChipESP32C3, 11, "vdd-spi"},
	}
	for _, c := range cases {
		mc, _ := newRecordingConn()
		f := flasherFor(c.chip, mc)
		reserved, reason := f.GPIOReserved(c.pin)
		if !reserved || reason != c.want {
			t.Errorf("%s pin %d: GPIOReserved = (%v, %q), want (true, %q)", c.chip, c.pin, reserved, reason, c.want)
		}
	}
}

func TestGPIOReservedUnreservedPin(t *testing.T) {
	mc, _ := newRecordingConn()
	f := flasherFor(ChipESP32C3, mc)
	if reserved, reason := f.GPIOReserved(5); reserved {
		t.Errorf("C3 pin 5: GPIOReserved = (true, %q), want (false, \"\")", reason)
	}
}

// --- Negative pins are rejected on every entry point ---

func TestNegativePinErrorsOnAllMethods(t *testing.T) {
	mc, _ := newRecordingConn()
	f := flasherFor(ChipESP32, mc)

	if err := f.SetGPIO(-1, true); err == nil {
		t.Error("SetGPIO: expected error for negative pin")
	}
	if _, err := f.ReadGPIO(-1); err == nil {
		t.Error("ReadGPIO: expected error for negative pin")
	}
	if err := f.ReleaseGPIO(-1); err == nil {
		t.Error("ReleaseGPIO: expected error for negative pin")
	}
	if reserved, reason := f.GPIOReserved(-1); !reserved || reason != "nonexistent" {
		t.Errorf("GPIOReserved(-1) = (%v, %q), want (true, \"nonexistent\")", reserved, reason)
	}
}

// --- readReg/writeReg errors propagate, wrapped, from every method ---

func TestReadGPIOReadRegError(t *testing.T) {
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			return 0, errors.New("port closed")
		},
	}
	f := flasherFor(ChipESP32, mc)

	if _, err := f.ReadGPIO(16); err == nil {
		t.Error("ReadGPIO: expected wrapped error, got nil")
	}
}

func TestReadGPIOWriteRegError(t *testing.T) {
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			return 0, nil
		},
		writeRegFunc: func(addr, value, mask, delayUS uint32) error {
			return errors.New("port closed")
		},
	}
	f := flasherFor(ChipESP32, mc)

	if _, err := f.ReadGPIO(16); err == nil {
		t.Error("ReadGPIO: expected wrapped error, got nil")
	}
}

// TestReadGPIODisableOutputWriteRegError fails the second writeReg call
// (ENABLE_W1TC, disabling the output driver) exercising that error-wrapping
// branch specifically, distinct from the IO_MUX write and IN read paths.
func TestReadGPIODisableOutputWriteRegError(t *testing.T) {
	writes := 0
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			return 0, nil
		},
		writeRegFunc: func(addr, value, mask, delayUS uint32) error {
			writes++
			if writes == 2 {
				return errors.New("port closed")
			}
			return nil
		},
	}
	f := flasherFor(ChipESP32, mc)

	if _, err := f.ReadGPIO(16); err == nil {
		t.Error("ReadGPIO: expected wrapped error for disable-output write failure, got nil")
	}
}

// TestReadGPIOReadInRegError fails the second readReg call (the final IN
// read), distinct from the first readReg call (IO_MUX read).
func TestReadGPIOReadInRegError(t *testing.T) {
	reads := 0
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			reads++
			if reads == 2 {
				return 0, errors.New("port closed")
			}
			return 0, nil
		},
	}
	f := flasherFor(ChipESP32, mc)

	if _, err := f.ReadGPIO(16); err == nil {
		t.Error("ReadGPIO: expected wrapped error for IN read failure, got nil")
	}
}

func TestSetGPIOReadRegError(t *testing.T) {
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			return 0, errors.New("port closed")
		},
	}
	f := flasherFor(ChipESP32, mc)

	if err := f.SetGPIO(16, true); err == nil {
		t.Error("SetGPIO: expected wrapped error, got nil")
	}
}

func TestSetGPIOWriteRegError(t *testing.T) {
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			return 0, nil
		},
		writeRegFunc: func(addr, value, mask, delayUS uint32) error {
			return errors.New("port closed")
		},
	}
	f := flasherFor(ChipESP32, mc)

	if err := f.SetGPIO(16, true); err == nil {
		t.Error("SetGPIO: expected wrapped error, got nil")
	}
}

// TestSetGPIOWriteRegErrorAtEachStep fails writeReg on the Nth write call to
// exercise every writeReg error-wrapping branch inside SetGPIO (IO_MUX,
// OUT_SEL, OUT level, and enable).
func TestSetGPIOWriteRegErrorAtEachStep(t *testing.T) {
	for _, failAt := range []int{1, 2, 3, 4} {
		writes := 0
		mc := &mockConnection{
			readRegFunc: func(addr uint32) (uint32, error) {
				return 0, nil
			},
			writeRegFunc: func(addr, value, mask, delayUS uint32) error {
				writes++
				if writes == failAt {
					return errors.New("port closed")
				}
				return nil
			},
		}
		f := flasherFor(ChipESP32, mc)

		if err := f.SetGPIO(16, true); err == nil {
			t.Errorf("SetGPIO: expected error when write #%d fails, got nil", failAt)
		}
	}
}

// TestSetGPIOWriteRegErrorClearOUT fails the third writeReg call (clear OUT)
// with level=false, exercising the OUT_W1TC error-wrapping branch.
func TestSetGPIOWriteRegErrorClearOUT(t *testing.T) {
	writes := 0
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			return 0, nil
		},
		writeRegFunc: func(addr, value, mask, delayUS uint32) error {
			writes++
			if writes == 3 {
				return errors.New("port closed")
			}
			return nil
		},
	}
	f := flasherFor(ChipESP32, mc)

	if err := f.SetGPIO(16, false); err == nil {
		t.Error("SetGPIO: expected wrapped error for clear-OUT write failure, got nil")
	}
}

// TestSetGPIOReadOutSelRegError fails the OUT_SEL readReg (second readReg
// call) specifically.
func TestSetGPIOReadOutSelRegError(t *testing.T) {
	reads := 0
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			reads++
			if reads == 2 {
				return 0, errors.New("port closed")
			}
			return 0, nil
		},
	}
	f := flasherFor(ChipESP32, mc)

	if err := f.SetGPIO(16, true); err == nil {
		t.Error("SetGPIO: expected wrapped error for OUT_SEL read failure, got nil")
	}
}

func TestReleaseGPIOWriteRegError(t *testing.T) {
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			return 0, nil
		},
		writeRegFunc: func(addr, value, mask, delayUS uint32) error {
			return errors.New("port closed")
		},
	}
	f := flasherFor(ChipESP32, mc)

	if err := f.ReleaseGPIO(16); err == nil {
		t.Error("ReleaseGPIO: expected wrapped error, got nil")
	}
}

// TestReleaseGPIOWriteIOMuxRegError fails the second writeReg call (the
// final IO_MUX write restoring FUN_IE), distinct from the first writeReg
// call (ENABLE_W1TC).
func TestReleaseGPIOWriteIOMuxRegError(t *testing.T) {
	writes := 0
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			return 0, nil
		},
		writeRegFunc: func(addr, value, mask, delayUS uint32) error {
			writes++
			if writes == 2 {
				return errors.New("port closed")
			}
			return nil
		},
	}
	f := flasherFor(ChipESP32, mc)

	if err := f.ReleaseGPIO(16); err == nil {
		t.Error("ReleaseGPIO: expected wrapped error for final IO_MUX write failure, got nil")
	}
}

func TestReleaseGPIOReadRegError(t *testing.T) {
	calls := 0
	mc := &mockConnection{
		readRegFunc: func(addr uint32) (uint32, error) {
			calls++
			return 0, errors.New("port closed")
		},
	}
	f := flasherFor(ChipESP32, mc)

	if err := f.ReleaseGPIO(16); err == nil {
		t.Error("ReleaseGPIO: expected wrapped error, got nil")
	}
	if calls == 0 {
		t.Error("expected readReg (IO_MUX) to be called before ReleaseGPIO returns")
	}
}

// --- Unsupported chip ---

func TestGPIOUnsupportedChip(t *testing.T) {
	mc, _ := newRecordingConn()
	f := flasherFor(ChipESP8266, mc)

	if err := f.SetGPIO(2, true); err == nil {
		t.Error("SetGPIO: expected error for unsupported chip")
	}
	if _, err := f.ReadGPIO(2); err == nil {
		t.Error("ReadGPIO: expected error for unsupported chip")
	}
	if err := f.ReleaseGPIO(2); err == nil {
		t.Error("ReleaseGPIO: expected error for unsupported chip")
	}
	if reserved, reason := f.GPIOReserved(2); !reserved || reason == "" {
		t.Errorf("GPIOReserved = (%v, %q), want (true, non-empty)", reserved, reason)
	}
}

// --- ESP32-S3, pin 2: sentinel 256, PIN_FUNC_GPIO=1, low-word pin ---

const (
	s3Pin2IOMux  = 0x60009000 + 0x0c
	s3Pin2OutSel = 0x60004554 + 4*2
)

func TestSetGPIOS3Low(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32S3, mc)

	if err := f.SetGPIO(2, true); err != nil {
		t.Fatalf("SetGPIO: %v", err)
	}
	assertOps(t, *ops, []regOp{
		{"r", s3Pin2IOMux, 0},
		{"w", s3Pin2IOMux, 1 << 12},
		{"r", s3Pin2OutSel, 0},
		{"w", s3Pin2OutSel, 256},
		{"w", 0x60004008, 1 << 2}, // OUT_W1TS
		{"w", 0x60004024, 1 << 2}, // ENABLE_W1TS
	})
}

func TestSetGPIOS3High(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32S3, mc)

	if err := f.SetGPIO(2, false); err != nil {
		t.Fatalf("SetGPIO: %v", err)
	}
	assertOps(t, *ops, []regOp{
		{"r", s3Pin2IOMux, 0},
		{"w", s3Pin2IOMux, 1 << 12},
		{"r", s3Pin2OutSel, 0},
		{"w", s3Pin2OutSel, 256},
		{"w", 0x6000400C, 1 << 2}, // OUT_W1TC
		{"w", 0x60004024, 1 << 2}, // ENABLE_W1TS
	})
}

// --- ESP32-S3, pin 38: high-word regs (OUT1/ENABLE1/IN1), PIN_FUNC_GPIO=1 ---

const (
	s3Pin38IOMux  = 0x60009000 + 0x9c
	s3Pin38OutSel = 0x60004554 + 4*38
)

func TestSetGPIOS3HighWordPin(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32S3, mc)

	if err := f.SetGPIO(38, true); err != nil {
		t.Fatalf("SetGPIO: %v", err)
	}
	assertOps(t, *ops, []regOp{
		{"r", s3Pin38IOMux, 0},
		{"w", s3Pin38IOMux, 1 << 12},
		{"r", s3Pin38OutSel, 0},
		{"w", s3Pin38OutSel, 256},
		{"w", 0x60004014, 1 << 6}, // OUT1_W1TS, bit = 38-32
		{"w", 0x60004030, 1 << 6}, // ENABLE1_W1TS
	})
}

func TestReadGPIOS3Low(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32S3, mc)

	if _, err := f.ReadGPIO(2); err != nil {
		t.Fatalf("ReadGPIO: %v", err)
	}
	assertOps(t, *ops, []regOp{
		{"r", s3Pin2IOMux, 0},
		{"w", s3Pin2IOMux, (1 << 12) | ioMuxFunIEBit},
		{"w", 0x60004028, 1 << 2}, // ENABLE_W1TC
		{"r", 0x6000403C, 0},      // IN
	})
}

func TestReadGPIOS3HighWordPin(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32S3, mc)

	if _, err := f.ReadGPIO(38); err != nil {
		t.Fatalf("ReadGPIO: %v", err)
	}
	assertOps(t, *ops, []regOp{
		{"r", s3Pin38IOMux, 0},
		{"w", s3Pin38IOMux, (1 << 12) | ioMuxFunIEBit},
		{"w", 0x60004034, 1 << 6}, // ENABLE1_W1TC, bit = 38-32
		{"r", 0x60004040, 0},      // IN1
	})
}

func TestReleaseGPIOS3HighWordPin(t *testing.T) {
	mc, ops := newRecordingConn()
	f := flasherFor(ChipESP32S3, mc)

	if err := f.ReleaseGPIO(38); err != nil {
		t.Fatalf("ReleaseGPIO: %v", err)
	}
	assertOps(t, *ops, []regOp{
		{"w", 0x60004034, 1 << 6}, // ENABLE1_W1TC
		{"r", s3Pin38IOMux, 0},
		{"w", s3Pin38IOMux, ioMuxFunIEBit},
	})
}

// --- ESP32-S3 IO_MUX offsets are linear across the 22-25 gap ---

func TestS3IOMuxLinearOffsets(t *testing.T) {
	cases := []struct {
		pin      int
		wantAddr uint32
	}{
		{2, 0x60009000 + 0x0c},
		{38, 0x60009000 + 0x9c},
	}
	for _, c := range cases {
		mc, ops := newRecordingConn()
		f := flasherFor(ChipESP32S3, mc)
		if _, err := f.ReadGPIO(c.pin); err != nil {
			t.Fatalf("pin %d: ReadGPIO: %v", c.pin, err)
		}
		if (*ops)[0].addr != c.wantAddr {
			t.Errorf("GPIO%d IO_MUX addr = %#x, want %#x", c.pin, (*ops)[0].addr, c.wantAddr)
		}
	}
}

// --- ESP32-S3 nonexistent pins 22-25 rejected on every method ---

func TestS3NonexistentPinsRejected(t *testing.T) {
	for _, pin := range []int{22, 23, 24, 25} {
		mc, _ := newRecordingConn()
		f := flasherFor(ChipESP32S3, mc)

		if err := f.SetGPIO(pin, true); err == nil {
			t.Errorf("pin %d: SetGPIO expected error", pin)
		}
		if _, err := f.ReadGPIO(pin); err == nil {
			t.Errorf("pin %d: ReadGPIO expected error", pin)
		}
		if err := f.ReleaseGPIO(pin); err == nil {
			t.Errorf("pin %d: ReleaseGPIO expected error", pin)
		}
		reserved, reason := f.GPIOReserved(pin)
		if !reserved || reason != "nonexistent" {
			t.Errorf("pin %d: GPIOReserved = (%v, %q), want (true, \"nonexistent\")", pin, reserved, reason)
		}
	}
}

// --- ESP32-S3 GPIOReserved classifications ---

func TestGPIOReservedClassificationsS3(t *testing.T) {
	cases := []struct {
		pin  int
		want string
	}{
		{26, "flash/psram"},
		{33, "flash/psram"},
		{45, "strap"},
		{19, "usb-jtag"},
		{43, "uart0"},
		{47, "differential-clock octal PSRAM"},
		{48, "differential-clock octal PSRAM"},
	}
	for _, c := range cases {
		mc, _ := newRecordingConn()
		f := flasherFor(ChipESP32S3, mc)
		reserved, reason := f.GPIOReserved(c.pin)
		if !reserved || reason != c.want {
			t.Errorf("S3 pin %d: GPIOReserved = (%v, %q), want (true, %q)", c.pin, reserved, reason, c.want)
		}
	}
}

func TestGPIOReservedJTAGNotReservedS3(t *testing.T) {
	mc, _ := newRecordingConn()
	f := flasherFor(ChipESP32S3, mc)
	if reserved, reason := f.GPIOReserved(40); reserved {
		t.Errorf("S3 pin 40 (JTAG): GPIOReserved = (true, %q), want (false, \"\")", reason)
	}
}
