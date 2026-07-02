package espflasher

import (
	"fmt"
	"time"
)

// ESP32-S2 register addresses for USB interface detection and the RTC
// watchdog reset used to exit native USB-OTG download mode.
// Reference: esptool/targets/esp32s2.py (ESP32S2ROM)
const (
	esp32s2UARTDevBufNo       uint32 = 0x3FFFFD14 // ROM .bss: active console interface
	esp32s2UARTDevBufNoUSBOTG uint32 = 2          // USB-OTG active

	// RTC_CNTL watchdog registers used by watchdog_reset() to force a
	// system reset. ESP32-S2 has no USB-Serial-JTAG bridge, so the
	// DTR/RTS latch trick used on S3/C3/C6/H2/C5 (hardResetUSB) is a
	// no-op here; esptool instead arms and lets the RTC WDT fire.
	esp32s2RTCCntlWDTConfig0  uint32 = 0x3F408094
	esp32s2RTCCntlWDTConfig1  uint32 = 0x3F408098
	esp32s2RTCCntlWDTWProtect uint32 = 0x3F4080AC
	esp32s2RTCCntlWDTWKey     uint32 = 0x50D83AA1

	// esp32s2WDTConfig0EnableValue is the RTC_CNTL_WDTCONFIG0 value used to
	// arm the watchdog for a system reset. It is copied verbatim from
	// esptool's ESP32S2ROM.watchdog_reset() and treated as an opaque,
	// HW-validated magic value rather than a precise bitfield breakdown of
	// the ESP32-S2 TRM register layout.
	esp32s2WDTConfig0EnableValue uint32 = (1 << 31) | (5 << 28) | (1 << 8) | 2
	// esp32s2WDTConfig1TimeoutTicks is the stage 0 timeout, in RTC_CLK
	// ticks (esptool uses 2000, i.e. a fast timeout since XTAL/RTC_CLK
	// runs the RTC watchdog).
	esp32s2WDTConfig1TimeoutTicks uint32 = 2000

	// GPIO strapping / RTC_CNTL_OPTION1 registers used to gate the
	// watchdog reset: if the chip is strapped for download boot, or
	// download mode is force-enabled, a watchdog reset would just put
	// it right back into the bootloader, so the caller must fall back
	// to the DTR/RTS path instead.
	esp32s2GPIOStrapReg             uint32 = 0x3F404038
	esp32s2GPIOStrapSPIBootMask     uint32 = 1 << 3
	esp32s2RTCCntlOption1Reg        uint32 = 0x3F408128
	esp32s2RTCCntlForceDownloadBoot uint32 = 0x1
)

// ESP32-S2 target definition.
// Reference: https://github.com/espressif/esptool/blob/master/esptool/targets/esp32s2.py

var defESP32S2 = &chipDef{
	ChipType:       ChipESP32S2,
	Name:           "ESP32-S2",
	MagicValue:     0x000007C6,
	ImageChipID:    2,
	UsesMagicValue: true,

	SPIRegBase:  0x3F402000,
	SPIUSROffs:  0x18,
	SPIUSR1Offs: 0x1C,
	SPIUSR2Offs: 0x20,
	SPIMOSIOffs: 0x24,
	SPIMISOOffs: 0x98,
	SPIW0Offs:   0x58,

	SPIMISODLenOffs: 0x28,
	SPIMOSIDLenOffs: 0x24,

	SPIAddrRegMSB: true,

	UARTDateReg: 0x60000078,
	UARTClkDiv:  0x3F400014,
	XTALClkDiv:  1,

	BootloaderFlashOffset: 0x1000,

	SupportsEncryptedFlash: true,
	ROMHasCompressedFlash:  true,
	ROMHasChangeBaud:       true,

	FlashFrequency: map[string]byte{
		"80m": 0xF,
		"40m": 0x0,
		"26m": 0x1,
		"20m": 0x2,
	},

	FlashSizes: defaultFlashSizes(),

	PostConnect:  esp32s2PostConnect,
	HardResetOTG: esp32s2HardReset,
}

// esp32s2PostConnect detects the USB interface type.
// ESP32-S2 only has USB-OTG (no USB-JTAG/Serial), and does not require
// watchdog disable for USB operation.
// Reference: esptool/targets/esp32s2.py _post_connect()
func esp32s2PostConnect(f *Flasher) error {
	uartDev, err := f.ReadRegister(esp32s2UARTDevBufNo)
	if err != nil {
		// In secure download mode, the register may be unreadable.
		// Default to non-USB behavior (safe fallback).
		return nil
	}

	if uartDev == esp32s2UARTDevBufNoUSBOTG {
		f.usesUSB = true
		f.logf("USB-OTG interface detected")
	}

	return nil
}

// esp32s2HardReset attempts an RTC watchdog reset to exit native USB-OTG
// download mode, mirroring esptool's ESP32S2ROM.hard_reset(). It is only
// applicable when the USB-OTG interface was detected (f.usesUSB); it
// returns false (fall back to the DTR/RTS hardReset path) if the interface
// isn't OTG, if the strap/force-download safety gate indicates a watchdog
// reset wouldn't reliably exit download mode, or if the register access
// fails (e.g. secure download mode).
func esp32s2HardReset(f *Flasher) bool {
	if !f.usesUSB {
		return false
	}

	strap, err := f.ReadRegister(esp32s2GPIOStrapReg)
	if err != nil {
		return false
	}
	option1, err := f.ReadRegister(esp32s2RTCCntlOption1Reg)
	if err != nil {
		return false
	}

	if strap&esp32s2GPIOStrapSPIBootMask != 0 || option1&esp32s2RTCCntlForceDownloadBoot != 0 {
		// GPIO0 is strapped high (SPI-boot strap set), or RTC_CNTL force-download-boot
		// is set: either condition means a watchdog reset would not cleanly exit to
		// the app and would land back in the bootloader instead, so fall back to
		// the DTR/RTS reset path.
		return false
	}

	if err := esp32s2WatchdogReset(f); err != nil {
		f.logf("watchdog reset failed, falling back to DTR/RTS reset: %v", err)
		return false
	}

	return true
}

// esp32s2WatchdogReset arms the RTC watchdog to force a system reset,
// mirroring esptool's ESP32S2ROM.watchdog_reset(). Reference:
// esptool/targets/esp32s2.py watchdog_reset().
func esp32s2WatchdogReset(f *Flasher) error {
	if err := f.WriteRegister(esp32s2RTCCntlWDTWProtect, esp32s2RTCCntlWDTWKey); err != nil {
		return fmt.Errorf("unlock RTC WDT: %w", err)
	}
	if err := f.WriteRegister(esp32s2RTCCntlWDTConfig1, esp32s2WDTConfig1TimeoutTicks); err != nil {
		return fmt.Errorf("set RTC WDT timeout: %w", err)
	}
	if err := f.WriteRegister(esp32s2RTCCntlWDTConfig0, esp32s2WDTConfig0EnableValue); err != nil {
		return fmt.Errorf("enable RTC WDT: %w", err)
	}
	if err := f.WriteRegister(esp32s2RTCCntlWDTWProtect, 0); err != nil {
		return fmt.Errorf("lock RTC WDT: %w", err)
	}

	f.logf("Hard resetting with a watchdog...")
	time.Sleep(500 * time.Millisecond) // wait for reset to take effect

	return nil
}
