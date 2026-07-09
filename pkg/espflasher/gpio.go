package espflasher

import "fmt"

// GPIO register field constants shared across supported chips.
const (
	// ioMuxMCUSelShift is the bit offset of the MCU_SEL field within an
	// IO_MUX pin configuration register.
	ioMuxMCUSelShift = 12
	// ioMuxMCUSelMask covers the 3-bit MCU_SEL field, bits[14:12].
	ioMuxMCUSelMask = uint32(0x7) << ioMuxMCUSelShift
	// ioMuxFunIEBit is the input-enable bit, bit[9].
	ioMuxFunIEBit = uint32(1) << 9
)

// gpioChipDef holds the GPIO matrix and IO_MUX register layout for one chip
// family. Only chips with a supported layout have an entry in gpioChipDefs.
type gpioChipDef struct {
	ioMuxBase uint32

	outW1TS, outW1TC   uint32
	out1W1TS, out1W1TC uint32 // 0 if the chip has no high-word registers

	enableW1TS, enableW1TC   uint32
	enable1W1TS, enable1W1TC uint32 // 0 if the chip has no high-word registers

	in, in1 uint32 // in1 0 if the chip has no high-word register

	hasHighWord bool // true if pins >31 exist and use the *1 registers

	funcOutSelBase uint32 // FUNC0_OUT_SEL_CFG; stride +4/pin
	outSelSentinel uint32 // direct GPIO-matrix output sentinel value
	outSelMask     uint32 // low-field mask of FUNCn_OUT_SEL_CFG (bits holding OUT_SEL)

	pinFuncGPIO uint32 // MCU_SEL value selecting the GPIO function
	maxPin      int

	ioMuxOffset map[int]uint32 // pin -> offset from ioMuxBase; absent = nonexistent
	reserved    map[int]string // pin -> reservation class (includes input-only)
	inputOnly   map[int]bool   // pins with no output driver
}

// gpioChipDefs maps supported chips to their GPIO register layouts.
// Only ESP32, ESP32-S2, ESP32-C3, and ESP32-S3 are currently supported.
var gpioChipDefs = map[ChipType]*gpioChipDef{
	ChipESP32:   defESP32GPIO,
	ChipESP32S2: defESP32S2GPIO,
	ChipESP32C3: defESP32C3GPIO,
	ChipESP32S3: defESP32S3GPIO,
}

var defESP32GPIO = &gpioChipDef{
	ioMuxBase:      0x3FF49000,
	outW1TS:        0x3FF44008,
	outW1TC:        0x3FF4400C,
	out1W1TS:       0x3FF44014,
	out1W1TC:       0x3FF44018,
	enableW1TS:     0x3FF44024,
	enableW1TC:     0x3FF44028,
	enable1W1TS:    0x3FF44030,
	enable1W1TC:    0x3FF44034,
	in:             0x3FF4403C,
	in1:            0x3FF44040,
	hasHighWord:    true,
	funcOutSelBase: 0x3FF44530,
	outSelSentinel: 256,
	outSelMask:     0x1FF,
	pinFuncGPIO:    2,
	maxPin:         39,
	ioMuxOffset: map[int]uint32{
		0: 0x44, 1: 0x88, 2: 0x40, 3: 0x84, 4: 0x48, 5: 0x6c, 6: 0x60, 7: 0x64,
		8: 0x68, 9: 0x54, 10: 0x58, 11: 0x5c, 12: 0x34, 13: 0x38, 14: 0x30, 15: 0x3c,
		16: 0x4c, 17: 0x50, 18: 0x70, 19: 0x74, 21: 0x7c, 22: 0x80, 23: 0x8c,
		25: 0x24, 26: 0x28, 27: 0x2c,
		32: 0x1c, 33: 0x20, 34: 0x14, 35: 0x18, 36: 0x04, 37: 0x08, 38: 0x0c, 39: 0x10,
		// Pin 20 exists only on PICO-package variants; treated as nonexistent
		// since the flasher cannot distinguish package variants.
		// Pins 24, 28-31 do not exist on any ESP32 package.
	},
	reserved: map[int]string{
		6: "flash", 7: "flash", 8: "flash", 9: "flash", 10: "flash", 11: "flash",
		0: "strap", 2: "strap", 5: "strap", 12: "strap", 15: "strap",
		1: "uart0", 3: "uart0",
		34: "input-only", 35: "input-only", 36: "input-only", 37: "input-only",
		38: "input-only", 39: "input-only",
	},
	inputOnly: map[int]bool{34: true, 35: true, 36: true, 37: true, 38: true, 39: true},
}

var defESP32S2GPIO = &gpioChipDef{
	ioMuxBase:      0x3F409000,
	outW1TS:        0x3F404008,
	outW1TC:        0x3F40400C,
	out1W1TS:       0x3F404014,
	out1W1TC:       0x3F404018,
	enableW1TS:     0x3F404024,
	enableW1TC:     0x3F404028,
	enable1W1TS:    0x3F404030,
	enable1W1TC:    0x3F404034,
	in:             0x3F40403C,
	in1:            0x3F404040,
	hasHighWord:    true,
	funcOutSelBase: 0x3F404554,
	outSelSentinel: 256,
	outSelMask:     0x1FF,
	pinFuncGPIO:    1,
	maxPin:         46,
	ioMuxOffset:    esp32s2IOMuxOffsets(),
	reserved: map[int]string{
		26: "flash/psram", 27: "flash/psram", 28: "flash/psram", 29: "flash/psram",
		30: "flash/psram", 31: "flash/psram", 32: "flash/psram",
		0: "strap", 45: "strap", 46: "strap",
		43: "uart0", 44: "uart0",
		// 46 is both a strap and input-only; reserved map keeps the strap
		// classification, inputOnly below still gates SetGPIO independently.
	},
	inputOnly: map[int]bool{46: true},
}

// esp32s2IOMuxOffsets builds the ESP32-S2 IO_MUX pin offset table:
// pins 0-21 are linear (0x04 + 4*n), pins 26-46 are linear from a different
// base (0x6C + 4*(n-26)). Pins 22-25 do not exist.
func esp32s2IOMuxOffsets() map[int]uint32 {
	offs := make(map[int]uint32, 22+21)
	for n := 0; n <= 21; n++ {
		offs[n] = 0x04 + 4*uint32(n)
	}
	for n := 26; n <= 46; n++ {
		offs[n] = 0x6C + 4*uint32(n-26)
	}
	return offs
}

var defESP32C3GPIO = &gpioChipDef{
	ioMuxBase:      0x60009000,
	outW1TS:        0x60004008,
	outW1TC:        0x6000400C,
	enableW1TS:     0x60004024,
	enableW1TC:     0x60004028,
	in:             0x6000403C,
	hasHighWord:    false,
	funcOutSelBase: 0x60004554,
	outSelSentinel: 128,
	outSelMask:     0xFF,
	pinFuncGPIO:    1,
	maxPin:         21,
	ioMuxOffset:    esp32c3IOMuxOffsets(),
	reserved: map[int]string{
		12: "flash", 13: "flash", 14: "flash", 15: "flash", 16: "flash", 17: "flash",
		2: "strap", 8: "strap", 9: "strap",
		11: "vdd-spi",
		18: "usb-jtag", 19: "usb-jtag",
		20: "uart0", 21: "uart0",
	},
	inputOnly: map[int]bool{},
}

// esp32c3IOMuxOffsets builds the ESP32-C3 IO_MUX pin offset table: linear,
// IO_MUX_GPIOn = 0x04 + 4*n for n=0..21.
func esp32c3IOMuxOffsets() map[int]uint32 {
	offs := make(map[int]uint32, 22)
	for n := 0; n <= 21; n++ {
		offs[n] = 0x04 + 4*uint32(n)
	}
	return offs
}

var defESP32S3GPIO = &gpioChipDef{
	ioMuxBase:      0x60009000,
	outW1TS:        0x60004008,
	outW1TC:        0x6000400C,
	out1W1TS:       0x60004014,
	out1W1TC:       0x60004018,
	enableW1TS:     0x60004024,
	enableW1TC:     0x60004028,
	enable1W1TS:    0x60004030,
	enable1W1TC:    0x60004034,
	in:             0x6000403C,
	in1:            0x60004040,
	hasHighWord:    true,
	funcOutSelBase: 0x60004554,
	outSelSentinel: 256,
	outSelMask:     0x1FF,
	pinFuncGPIO:    1,
	maxPin:         48,
	ioMuxOffset:    esp32s3IOMuxOffsets(),
	reserved: map[int]string{
		26: "flash/psram", 27: "flash/psram", 28: "flash/psram", 29: "flash/psram", 30: "flash/psram", 31: "flash/psram", 32: "flash/psram",
		33: "flash/psram", 34: "flash/psram", 35: "flash/psram", 36: "flash/psram", 37: "flash/psram",
		47: "differential-clock octal PSRAM", 48: "differential-clock octal PSRAM",
		0: "strap", 3: "strap", 45: "strap", 46: "strap",
		19: "usb-jtag", 20: "usb-jtag",
		43: "uart0", 44: "uart0",
		// JTAG pins 39-42 are intentionally left unreserved: drivable as
		// free GPIO when no debugger is attached.
	},
	inputOnly: map[int]bool{},
}

// esp32s3IOMuxOffsets builds the ESP32-S3 IO_MUX pin offset table: linear,
// IO_MUX_GPIOn = 0x04 + 4*n for n in {0..21, 26..48}. Pins 22-25 do not exist.
func esp32s3IOMuxOffsets() map[int]uint32 {
	offs := make(map[int]uint32, 22+23)
	for n := 0; n <= 21; n++ {
		offs[n] = 0x04 + 4*uint32(n)
	}
	for n := 26; n <= 48; n++ {
		offs[n] = 0x04 + 4*uint32(n)
	}
	return offs
}

// gpioDefFor returns the GPIO register layout for f's connected chip, or an
// error if the chip is not supported for GPIO operations.
func (f *Flasher) gpioDefFor() (*gpioChipDef, error) {
	ct := f.ChipType()
	def, ok := gpioChipDefs[ct]
	if !ok {
		return nil, fmt.Errorf("GPIO not supported for %s", ct)
	}
	return def, nil
}

// resolvePin validates that pin exists on the chip and returns its IO_MUX
// register address.
func (d *gpioChipDef) resolvePin(pin int) (ioMuxAddr uint32, err error) {
	if pin < 0 || pin > d.maxPin {
		return 0, fmt.Errorf("pin %d does not exist", pin)
	}
	off, ok := d.ioMuxOffset[pin]
	if !ok {
		return 0, fmt.Errorf("pin %d does not exist", pin)
	}
	return d.ioMuxBase + off, nil
}

// outEnableRegs returns the OUT_W1TS/W1TC and ENABLE_W1TS/W1TC register
// addresses and bit index to use for pin (selecting the *1 high-word
// registers for pin >31 on chips that have them).
func (d *gpioChipDef) outEnableRegs(pin int) (outW1TS, outW1TC, enW1TS, enW1TC uint32, bit uint32) {
	if pin >= 32 && d.hasHighWord {
		return d.out1W1TS, d.out1W1TC, d.enable1W1TS, d.enable1W1TC, uint32(pin - 32)
	}
	return d.outW1TS, d.outW1TC, d.enableW1TS, d.enableW1TC, uint32(pin)
}

// inReg returns the IN/IN1 register address and bit index to use for pin.
func (d *gpioChipDef) inReg(pin int) (addr uint32, bit uint32) {
	if pin >= 32 && d.hasHighWord {
		return d.in1, uint32(pin - 32)
	}
	return d.in, uint32(pin)
}

// funcOutSelAddr returns the FUNCn_OUT_SEL_CFG register address for pin.
func (d *gpioChipDef) funcOutSelAddr(pin int) uint32 {
	return d.funcOutSelBase + 4*uint32(pin)
}

// ReadGPIO configures pin as a GPIO input and returns its current level.
func (f *Flasher) ReadGPIO(pin int) (bool, error) {
	def, err := f.gpioDefFor()
	if err != nil {
		return false, err
	}
	ioMuxAddr, err := def.resolvePin(pin)
	if err != nil {
		return false, err
	}

	// RMW IO_MUX: MCU_SEL = PIN_FUNC_GPIO, FUN_IE = 1.
	val, err := f.conn.readReg(ioMuxAddr)
	if err != nil {
		return false, fmt.Errorf("read IO_MUX for pin %d: %w", pin, err)
	}
	val = (val &^ ioMuxMCUSelMask) | (def.pinFuncGPIO << ioMuxMCUSelShift)
	val |= ioMuxFunIEBit
	if err := f.conn.writeReg(ioMuxAddr, val, 0xFFFFFFFF, 0); err != nil {
		return false, fmt.Errorf("write IO_MUX for pin %d: %w", pin, err)
	}

	// Ensure the output driver is disabled so a prior SetGPIO (without a
	// matching ReleaseGPIO) cannot make this read observe the flasher's own
	// driven value instead of the pin's true input level.
	_, _, _, enW1TC, bit := def.outEnableRegs(pin)
	if err := f.conn.writeReg(enW1TC, 1<<bit, 0xFFFFFFFF, 0); err != nil {
		return false, fmt.Errorf("disable output for pin %d: %w", pin, err)
	}

	inAddr, bit := def.inReg(pin)
	inVal, err := f.conn.readReg(inAddr)
	if err != nil {
		return false, fmt.Errorf("read IN for pin %d: %w", pin, err)
	}
	return inVal&(1<<bit) != 0, nil
}

// SetGPIO configures pin as a GPIO output and drives it to level.
// Input-only pins and pins that do not exist are refused.
func (f *Flasher) SetGPIO(pin int, level bool) error {
	def, err := f.gpioDefFor()
	if err != nil {
		return err
	}
	ioMuxAddr, err := def.resolvePin(pin)
	if err != nil {
		return err
	}
	if def.inputOnly[pin] {
		return fmt.Errorf("pin %d is input-only", pin)
	}

	// RMW IO_MUX: MCU_SEL = PIN_FUNC_GPIO.
	val, err := f.conn.readReg(ioMuxAddr)
	if err != nil {
		return fmt.Errorf("read IO_MUX for pin %d: %w", pin, err)
	}
	val = (val &^ ioMuxMCUSelMask) | (def.pinFuncGPIO << ioMuxMCUSelShift)
	if err := f.conn.writeReg(ioMuxAddr, val, 0xFFFFFFFF, 0); err != nil {
		return fmt.Errorf("write IO_MUX for pin %d: %w", pin, err)
	}

	// RMW FUNCn_OUT_SEL_CFG: low field = direct-GPIO sentinel.
	selAddr := def.funcOutSelAddr(pin)
	selVal, err := f.conn.readReg(selAddr)
	if err != nil {
		return fmt.Errorf("read OUT_SEL for pin %d: %w", pin, err)
	}
	selVal = (selVal &^ def.outSelMask) | (def.outSelSentinel & def.outSelMask)
	if err := f.conn.writeReg(selAddr, selVal, 0xFFFFFFFF, 0); err != nil {
		return fmt.Errorf("write OUT_SEL for pin %d: %w", pin, err)
	}

	outW1TS, outW1TC, enW1TS, _, bit := def.outEnableRegs(pin)

	// Set the level first, then enable the output driver.
	if level {
		if err := f.conn.writeReg(outW1TS, 1<<bit, 0xFFFFFFFF, 0); err != nil {
			return fmt.Errorf("set OUT for pin %d: %w", pin, err)
		}
	} else {
		if err := f.conn.writeReg(outW1TC, 1<<bit, 0xFFFFFFFF, 0); err != nil {
			return fmt.Errorf("clear OUT for pin %d: %w", pin, err)
		}
	}

	if err := f.conn.writeReg(enW1TS, 1<<bit, 0xFFFFFFFF, 0); err != nil {
		return fmt.Errorf("enable output for pin %d: %w", pin, err)
	}
	return nil
}

// ReleaseGPIO disables the output driver on pin and leaves it readable as
// an input. MCU_SEL and OUT_SEL are left as-is.
func (f *Flasher) ReleaseGPIO(pin int) error {
	def, err := f.gpioDefFor()
	if err != nil {
		return err
	}
	ioMuxAddr, err := def.resolvePin(pin)
	if err != nil {
		return err
	}

	_, _, _, enW1TC, bit := def.outEnableRegs(pin)
	if err := f.conn.writeReg(enW1TC, 1<<bit, 0xFFFFFFFF, 0); err != nil {
		return fmt.Errorf("disable output for pin %d: %w", pin, err)
	}

	val, err := f.conn.readReg(ioMuxAddr)
	if err != nil {
		return fmt.Errorf("read IO_MUX for pin %d: %w", pin, err)
	}
	val |= ioMuxFunIEBit
	if err := f.conn.writeReg(ioMuxAddr, val, 0xFFFFFFFF, 0); err != nil {
		return fmt.Errorf("write IO_MUX for pin %d: %w", pin, err)
	}
	return nil
}

// GPIOReserved reports whether pin is reserved for a chip function that
// makes driving it unsafe or meaningless (nonexistent, flash/PSRAM, strapping,
// input-only, active UART0, or USB-JTAG), along with a short reason. It does
// not block SetGPIO/ReadGPIO/ReleaseGPIO except for nonexistent and
// input-only pins (SetGPIO) — callers decide whether to gate on the rest.
func (f *Flasher) GPIOReserved(pin int) (bool, string) {
	def, err := f.gpioDefFor()
	if err != nil {
		return true, err.Error()
	}
	if _, err := def.resolvePin(pin); err != nil {
		return true, "nonexistent"
	}
	if reason, ok := def.reserved[pin]; ok {
		return true, reason
	}
	if def.inputOnly[pin] {
		return true, "input-only"
	}
	return false, ""
}
