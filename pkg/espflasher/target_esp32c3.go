package espflasher

import (
	"fmt"
	"net"
)

// ESP32-C3 register addresses for USB interface detection and watchdog control.
// Reference: esptool/targets/esp32c3.py
const (
	// UARTDEV_BUF_NO: ROM .bss variable holding the console interface in
	// use. Address matches esptool's ESP32C3ROM.
	esp32c3UARTDevBufNo              uint32 = 0x3FCDF07C
	esp32c3UARTDevBufNoUSBJTAGSerial uint32 = 3 // USB-JTAG/Serial active

	// RTC_CNTL_OPTION1 force-download-boot bit, set by the ROM on entering
	// download mode over USB-Serial/JTAG. Cleared before reset.
	esp32c3RTCCntlOption1Reg        uint32 = 0x600080F4
	esp32c3RTCCntlForceDownloadBoot uint32 = 0x1

	// RTC_CNTL watchdog registers (different offsets from S3).
	esp32c3RTCCntlWDTConfig0  uint32 = 0x60008090
	esp32c3RTCCntlWDTWProtect uint32 = 0x600080A8
	esp32c3RTCCntlWDTWKey     uint32 = 0x50D83AA1

	// Super Watchdog (SWD) registers.
	esp32c3RTCCntlSWDConf       uint32 = 0x600080AC
	esp32c3RTCCntlSWDAutoFeedEn uint32 = 1 << 31
	esp32c3RTCCntlSWDWProtect   uint32 = 0x600080B0
	esp32c3RTCCntlSWDWKey       uint32 = 0x8F1D312A

	// EFUSE_BLOCK1 base and words used for MAC/revision/feature decoding.
	// Reference: esptool/targets/esp32c3.py (EFUSE_BLOCK1_ADDR, MAC_EFUSE_REG,
	// get_major_chip_version/get_minor_chip_version/get_flash_cap/get_flash_vendor).
	esp32c3EfuseBlock1Word0 uint32 = 0x60008844 // MAC_EFUSE_REG (num_word 0)
	esp32c3EfuseBlock1Word3 uint32 = 0x60008850 // num_word 3
	esp32c3EfuseBlock1Word4 uint32 = 0x60008854 // num_word 4
	esp32c3EfuseBlock1Word5 uint32 = 0x60008858 // num_word 5
)

// ESP32-C3 target definition.
// Reference: https://github.com/espressif/esptool/blob/master/esptool/targets/esp32c3.py

var defESP32C3 = &chipDef{
	ChipType:       ChipESP32C3,
	Name:           "ESP32-C3",
	ImageChipID:    5,
	UsesMagicValue: false, // Uses chip ID

	SPIRegBase:  0x60002000,
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
	UARTClkDiv:  0x60000014,
	XTALClkDiv:  1,

	BootloaderFlashOffset: 0x0,

	SupportsEncryptedFlash: true,
	ROMHasCompressedFlash:  true,
	ROMHasChangeBaud:       true,

	FlashFrequency: map[string]byte{
		"80m": 0xF,
		"40m": 0x0,
		"20m": 0x2,
	},

	FlashSizes: defaultFlashSizes(),

	PostConnect: esp32c3PostConnect,

	ForceDownloadBootReg:  esp32c3RTCCntlOption1Reg,
	ForceDownloadBootMask: esp32c3RTCCntlForceDownloadBoot,

	ReadMAC:          esp32c3ReadMAC,
	ReadChipRevision: esp32c3ReadChipRevision,
	ReadChipFeatures: esp32c3ReadChipFeatures,
}

// esp32c3PostConnect detects the USB interface type and disables watchdogs
// when connected via USB-JTAG/Serial. Without this, the RTC WDT fires
// during flash and resets the chip mid-operation.
// Reference: esptool/targets/esp32c3.py _post_connect()
func esp32c3PostConnect(f *Flasher) error {
	// Prefer VID/PID (as esptool does): it doesn't depend on the ROM .bss
	// layout. Fall back to the ROM variable when the host reports no
	// VID/PID (macOS without cgo) or the port name doesn't match.
	usbJTAG := f.usbInterfaceFromPort() == usbInterfaceSerialJTAG
	if !usbJTAG {
		val, err := f.ReadRegister(esp32c3UARTDevBufNo)
		if err != nil {
			// In secure download mode, the register may be unreadable.
			// Default to non-USB behavior (safe fallback).
			return nil
		}
		usbJTAG = val == esp32c3UARTDevBufNoUSBJTAGSerial
	}

	if usbJTAG {
		f.usesUSB = true
		f.logf("USB-JTAG/Serial interface detected, disabling watchdogs")
		if err := disableWatchdogsESP32C3(f); err != nil {
			return err
		}
	}

	return nil
}

// disableWatchdogsESP32C3 disables the RTC WDT and enables SWD auto-feed.
// This prevents the watchdog from resetting the chip during flash operations
// when connected via USB-JTAG/Serial.
func disableWatchdogsESP32C3(f *Flasher) error {
	// Unlock and disable RTC WDT
	if err := f.WriteRegister(esp32c3RTCCntlWDTWProtect, esp32c3RTCCntlWDTWKey); err != nil {
		return fmt.Errorf("unlock RTC WDT: %w", err)
	}
	if err := f.WriteRegister(esp32c3RTCCntlWDTConfig0, 0); err != nil {
		return fmt.Errorf("disable RTC WDT: %w", err)
	}
	if err := f.WriteRegister(esp32c3RTCCntlWDTWProtect, 0); err != nil {
		return fmt.Errorf("lock RTC WDT: %w", err)
	}

	// Unlock SWD and enable auto-feed
	if err := f.WriteRegister(esp32c3RTCCntlSWDWProtect, esp32c3RTCCntlSWDWKey); err != nil {
		return fmt.Errorf("unlock SWD: %w", err)
	}

	swdConf, err := f.ReadRegister(esp32c3RTCCntlSWDConf)
	if err != nil {
		return fmt.Errorf("read SWD conf: %w", err)
	}
	swdConf |= esp32c3RTCCntlSWDAutoFeedEn
	if err := f.WriteRegister(esp32c3RTCCntlSWDConf, swdConf); err != nil {
		return fmt.Errorf("enable SWD auto-feed: %w", err)
	}
	if err := f.WriteRegister(esp32c3RTCCntlSWDWProtect, 0); err != nil {
		return fmt.Errorf("lock SWD: %w", err)
	}

	return nil
}

// esp32c3ReadMAC reads the factory-programmed base MAC from eFuse.
// Reference: esptool/targets/esp32c3.py read_mac().
func esp32c3ReadMAC(f *Flasher) (net.HardwareAddr, error) {
	word0, err := f.ReadRegister(esp32c3EfuseBlock1Word0)
	if err != nil {
		return nil, err
	}
	word1, err := f.ReadRegister(esp32c3EfuseBlock1Word0 + 4)
	if err != nil {
		return nil, err
	}
	return decodeEfuseMAC(word0, word1), nil
}

// esp32c3ReadChipRevision reads the eFuse-encoded silicon revision.
// Reference: esptool/targets/esp32c3.py get_major_chip_version()/
// get_minor_chip_version().
func esp32c3ReadChipRevision(f *Flasher) (ChipRevision, error) {
	word3, err := f.ReadRegister(esp32c3EfuseBlock1Word3)
	if err != nil {
		return ChipRevision{}, err
	}
	word5, err := f.ReadRegister(esp32c3EfuseBlock1Word5)
	if err != nil {
		return ChipRevision{}, err
	}
	major := (word5 >> 24) & 0x3
	hi := (word5 >> 23) & 0x1
	low := (word3 >> 18) & 0x7
	minor := (hi << 3) + low
	return ChipRevision{Major: int(major), Minor: int(minor)}, nil
}

// esp32c3ReadChipFeatures returns the chip feature list.
// Reference: esptool/targets/esp32c3.py get_chip_features().
func esp32c3ReadChipFeatures(f *Flasher) ([]string, error) {
	word3, err := f.ReadRegister(esp32c3EfuseBlock1Word3)
	if err != nil {
		return nil, err
	}
	word4, err := f.ReadRegister(esp32c3EfuseBlock1Word4)
	if err != nil {
		return nil, err
	}

	features := []string{"Wi-Fi", "BT 5 (LE)", "Single Core", "160MHz"}

	flashCap := (word3 >> 27) & 0x7
	flash, ok := map[uint32]string{
		1: "Embedded Flash 4MB",
		2: "Embedded Flash 2MB",
		3: "Embedded Flash 1MB",
		4: "Embedded Flash 8MB",
	}[flashCap]
	if !ok && flashCap != 0 {
		flash = "Unknown Embedded Flash"
	}
	if flash != "" {
		vendorID := word4 & 0x7
		vendor := map[uint32]string{
			1: "XMC",
			2: "GD",
			3: "FM",
			4: "TT",
			5: "ZBIT",
		}[vendorID]
		features = append(features, fmt.Sprintf("%s (%s)", flash, vendor))
	}

	return features, nil
}
