package espflasher

import (
	"fmt"
	"net"
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

	// EFUSE_BLOCK1/BLOCK2 words used for MAC/revision/feature decoding.
	// Reference: esptool/targets/esp32s2.py (EFUSE_BLOCK1_ADDR,
	// EFUSE_BLOCK2_ADDR, MAC_EFUSE_REG, get_major_chip_version/
	// get_minor_chip_version/get_flash_cap/get_psram_cap/get_block2_version).
	esp32s2EfuseBlock1Word0 uint32 = 0x3F41A044 // MAC_EFUSE_REG (num_word 0)
	esp32s2EfuseBlock1Word3 uint32 = 0x3F41A050 // num_word 3
	esp32s2EfuseBlock1Word4 uint32 = 0x3F41A054 // num_word 4
	esp32s2EfuseBlock2Word4 uint32 = 0x3F41A06C // num_word 4 (BLK_VERSION_MINOR)
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

	ReadMAC:          esp32s2ReadMAC,
	ReadChipRevision: esp32s2ReadChipRevision,
	ReadChipFeatures: esp32s2ReadChipFeatures,
}

// esp32s2PostConnect detects the USB interface type.
// ESP32-S2 only has USB-OTG (no USB-JTAG/Serial), and does not require
// watchdog disable for USB operation.
// Reference: esptool/targets/esp32s2.py _post_connect()
func esp32s2PostConnect(f *Flasher) error {
	// Prefer VID/PID (as esptool does); fall back to the ROM variable when
	// the host reports none.
	usbOTG := f.usbInterfaceFromPort() == usbInterfaceOTG
	if !usbOTG {
		uartDev, err := f.ReadRegister(esp32s2UARTDevBufNo)
		if err != nil {
			// In secure download mode, the register may be unreadable.
			// Default to non-USB behavior (safe fallback).
			return nil
		}
		usbOTG = uartDev == esp32s2UARTDevBufNoUSBOTG
	}

	if usbOTG {
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

// esp32s2ReadMAC reads the factory-programmed base MAC from eFuse.
// Reference: esptool/targets/esp32s2.py read_mac().
func esp32s2ReadMAC(f *Flasher) (net.HardwareAddr, error) {
	word0, err := f.ReadRegister(esp32s2EfuseBlock1Word0)
	if err != nil {
		return nil, err
	}
	word1, err := f.ReadRegister(esp32s2EfuseBlock1Word0 + 4)
	if err != nil {
		return nil, err
	}
	return decodeEfuseMAC(word0, word1), nil
}

// esp32s2ReadChipRevision reads the eFuse-encoded silicon revision.
// Reference: esptool/targets/esp32s2.py get_major_chip_version()/
// get_minor_chip_version().
func esp32s2ReadChipRevision(f *Flasher) (ChipRevision, error) {
	word3, err := f.ReadRegister(esp32s2EfuseBlock1Word3)
	if err != nil {
		return ChipRevision{}, err
	}
	word4, err := f.ReadRegister(esp32s2EfuseBlock1Word4)
	if err != nil {
		return ChipRevision{}, err
	}
	major := (word3 >> 18) & 0x3
	hi := (word3 >> 20) & 0x1
	low := (word4 >> 4) & 0x7
	minor := (hi << 3) + low
	return ChipRevision{Major: int(major), Minor: int(minor)}, nil
}

// esp32s2ReadChipFeatures returns the chip feature list.
// Reference: esptool/targets/esp32s2.py get_chip_features().
func esp32s2ReadChipFeatures(f *Flasher) ([]string, error) {
	word3, err := f.ReadRegister(esp32s2EfuseBlock1Word3)
	if err != nil {
		return nil, err
	}
	block2Word4, err := f.ReadRegister(esp32s2EfuseBlock2Word4)
	if err != nil {
		return nil, err
	}

	flashCap := (word3 >> 21) & 0xF
	flashVersion, ok := map[uint32]string{
		0: "No Embedded Flash",
		1: "Embedded Flash 2MB",
		2: "Embedded Flash 4MB",
	}[flashCap]
	if !ok {
		flashVersion = "Unknown Embedded Flash"
	}

	psramCap := (word3 >> 28) & 0xF
	psramVersion, ok := map[uint32]string{
		0: "No Embedded PSRAM",
		1: "Embedded PSRAM 2MB",
		2: "Embedded PSRAM 4MB",
	}[psramCap]
	if !ok {
		psramVersion = "Unknown Embedded PSRAM"
	}

	block2Version := (block2Word4 >> 4) & 0x7
	block2VersionStr, ok := map[uint32]string{
		0: "No calibration in BLK2 of efuse",
		1: "ADC and temperature sensor calibration in BLK2 of eFuse V1",
		2: "ADC and temperature sensor calibration in BLK2 of eFuse V2",
	}[block2Version]
	if !ok {
		block2VersionStr = "Unknown calibration in BLK2"
	}

	return []string{
		"Wi-Fi",
		"Single Core",
		"240MHz",
		flashVersion,
		psramVersion,
		block2VersionStr,
	}, nil
}
