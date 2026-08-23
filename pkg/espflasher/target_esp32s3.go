package espflasher

import (
	"fmt"
	"net"
)

// ESP32-S3 register addresses for USB interface detection and watchdog control.
// Reference: esptool/targets/esp32s3.py
const (
	esp32s3UARTDevBufNo              uint32 = 0x3FCEF14C // ROM .bss: active console interface
	esp32s3UARTDevBufNoUSBOTG        uint32 = 3          // USB-OTG (CDC) active
	esp32s3UARTDevBufNoUSBJTAGSerial uint32 = 4          // USB-JTAG/Serial active

	// RTC_CNTL_OPTION1 force-download-boot bit, set by the ROM on entering
	// download mode over native USB. Cleared before reset.
	// Reference: esptool/targets/esp32s3.py hard_reset().
	esp32s3RTCCntlOption1Reg        uint32 = 0x6000812C
	esp32s3RTCCntlForceDownloadBoot uint32 = 0x1

	esp32s3RTCCntlWDTConfig0  uint32 = 0x60008098
	esp32s3RTCCntlWDTWProtect uint32 = 0x600080B0
	esp32s3RTCCntlWDTWKey     uint32 = 0x50D83AA1

	esp32s3RTCCntlSWDConf       uint32 = 0x600080B4
	esp32s3RTCCntlSWDAutoFeedEn uint32 = 1 << 31
	esp32s3RTCCntlSWDWProtect   uint32 = 0x600080B8
	esp32s3RTCCntlSWDWKey       uint32 = 0x8F1D312A

	// EFUSE_BLOCK1/BLOCK2 words used for MAC/revision/feature decoding.
	// Reference: esptool/targets/esp32s3.py (EFUSE_BLOCK1_ADDR,
	// EFUSE_BLOCK2_ADDR, MAC_EFUSE_REG, get_major_chip_version/
	// get_minor_chip_version/get_blk_version_major/get_blk_version_minor/
	// get_flash_cap/get_flash_vendor/get_psram_cap/get_psram_vendor).
	esp32s3EfuseBlock1Word0 uint32 = 0x60007044 // MAC_EFUSE_REG (num_word 0)
	esp32s3EfuseBlock1Word3 uint32 = 0x60007050 // num_word 3
	esp32s3EfuseBlock1Word4 uint32 = 0x60007054 // num_word 4
	esp32s3EfuseBlock1Word5 uint32 = 0x60007058 // num_word 5
	esp32s3EfuseBlock2Word4 uint32 = 0x6000706C // num_word 4 (BLK_VERSION_MAJOR)
)

// ESP32-S3 target definition.
// Reference: https://github.com/espressif/esptool/blob/master/esptool/targets/esp32s3.py

var defESP32S3 = &chipDef{
	ChipType:       ChipESP32S3,
	Name:           "ESP32-S3",
	ImageChipID:    9,
	UsesMagicValue: false, // Uses chip ID, not magic value

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

	PostConnect: esp32s3PostConnect,

	ForceDownloadBootReg:  esp32s3RTCCntlOption1Reg,
	ForceDownloadBootMask: esp32s3RTCCntlForceDownloadBoot,

	ReadMAC:          esp32s3ReadMAC,
	ReadChipRevision: esp32s3ReadChipRevision,
	ReadChipFeatures: esp32s3ReadChipFeatures,
}

// esp32s3PostConnect detects the USB interface type and disables watchdogs
// when connected via USB-JTAG/Serial. Without this, the RTC WDT fires
// during flash and resets the chip mid-operation.
// Reference: esptool/targets/esp32s3.py _post_connect()
func esp32s3PostConnect(f *Flasher) error {
	// Prefer VID/PID (as esptool does); fall back to the ROM variable when
	// the host reports none.
	val := uint32(0)
	switch f.usbInterfaceFromPort() {
	case usbInterfaceSerialJTAG:
		val = esp32s3UARTDevBufNoUSBJTAGSerial
	case usbInterfaceOTG:
		val = esp32s3UARTDevBufNoUSBOTG
	default:
		var err error
		val, err = f.conn.readReg(esp32s3UARTDevBufNo)
		if err != nil {
			// In secure download mode, the register may be unreadable.
			// Default to non-USB behavior (safe fallback).
			return nil
		}
	}

	switch val {
	case esp32s3UARTDevBufNoUSBJTAGSerial:
		f.usesUSB = true
		f.logf("USB-JTAG/Serial interface detected, disabling watchdogs")
		if err := disableWatchdogsESP32S3(f); err != nil {
			return err
		}
	case esp32s3UARTDevBufNoUSBOTG:
		f.usesUSB = true
		f.logf("USB-OTG interface detected")
	}

	return nil
}

// disableWatchdogsESP32S3 disables the RTC WDT and enables SWD auto-feed.
// This prevents the watchdog from resetting the chip during flash operations
// when connected via USB-JTAG/Serial.
func disableWatchdogsESP32S3(f *Flasher) error {
	// Unlock and disable RTC WDT
	if err := f.conn.writeReg(esp32s3RTCCntlWDTWProtect, esp32s3RTCCntlWDTWKey, 0xFFFFFFFF, 0); err != nil {
		return fmt.Errorf("unlock RTC WDT: %w", err)
	}
	if err := f.conn.writeReg(esp32s3RTCCntlWDTConfig0, 0, 0xFFFFFFFF, 0); err != nil {
		return fmt.Errorf("disable RTC WDT: %w", err)
	}
	if err := f.conn.writeReg(esp32s3RTCCntlWDTWProtect, 0, 0xFFFFFFFF, 0); err != nil {
		return fmt.Errorf("lock RTC WDT: %w", err)
	}

	// Unlock SWD and enable auto-feed
	if err := f.conn.writeReg(esp32s3RTCCntlSWDWProtect, esp32s3RTCCntlSWDWKey, 0xFFFFFFFF, 0); err != nil {
		return fmt.Errorf("unlock SWD: %w", err)
	}

	swdConf, err := f.conn.readReg(esp32s3RTCCntlSWDConf)
	if err != nil {
		return fmt.Errorf("read SWD conf: %w", err)
	}
	swdConf |= esp32s3RTCCntlSWDAutoFeedEn
	if err := f.conn.writeReg(esp32s3RTCCntlSWDConf, swdConf, 0xFFFFFFFF, 0); err != nil {
		return fmt.Errorf("enable SWD auto-feed: %w", err)
	}
	if err := f.conn.writeReg(esp32s3RTCCntlSWDWProtect, 0, 0xFFFFFFFF, 0); err != nil {
		return fmt.Errorf("lock SWD: %w", err)
	}

	return nil
}

// esp32s3ReadMAC reads the factory-programmed base MAC from eFuse.
// Reference: esptool/targets/esp32s3.py read_mac().
func esp32s3ReadMAC(f *Flasher) (net.HardwareAddr, error) {
	word0, err := f.ReadRegister(esp32s3EfuseBlock1Word0)
	if err != nil {
		return nil, err
	}
	word1, err := f.ReadRegister(esp32s3EfuseBlock1Word0 + 4)
	if err != nil {
		return nil, err
	}
	return decodeEfuseMAC(word0, word1), nil
}

// esp32s3IsEco0 mirrors esptool's ESP32S3ROM.is_eco0(): on v0.0 (ECO0)
// silicon the major-version field was reallocated for other purposes when
// the eFuse block version is v1.1, so the raw major/minor fields must be
// overridden to 0/0 rather than trusted directly.
// Reference: esptool/targets/esp32s3.py is_eco0()/get_major_chip_version()/
// get_minor_chip_version().
func esp32s3IsEco0(rawMinor, blkVerMajor, blkVerMinor uint32) bool {
	return (rawMinor&0x7) == 0 && blkVerMajor == 1 && blkVerMinor == 1
}

// esp32s3ReadChipRevision reads the eFuse-encoded silicon revision,
// including the ECO0 workaround (see esp32s3IsEco0).
// Reference: esptool/targets/esp32s3.py get_major_chip_version()/
// get_minor_chip_version()/get_raw_major_chip_version()/
// get_raw_minor_chip_version()/get_blk_version_major()/get_blk_version_minor().
func esp32s3ReadChipRevision(f *Flasher) (ChipRevision, error) {
	word3, err := f.ReadRegister(esp32s3EfuseBlock1Word3)
	if err != nil {
		return ChipRevision{}, err
	}
	word5, err := f.ReadRegister(esp32s3EfuseBlock1Word5)
	if err != nil {
		return ChipRevision{}, err
	}
	block2Word4, err := f.ReadRegister(esp32s3EfuseBlock2Word4)
	if err != nil {
		return ChipRevision{}, err
	}

	hi := (word5 >> 23) & 0x1
	low := (word3 >> 18) & 0x7
	rawMinor := (hi << 3) + low
	rawMajor := (word5 >> 24) & 0x3

	blkVerMajor := block2Word4 & 0x3
	blkVerMinor := (word3 >> 24) & 0x7

	if esp32s3IsEco0(rawMinor, blkVerMajor, blkVerMinor) {
		return ChipRevision{Major: 0, Minor: 0}, nil
	}
	return ChipRevision{Major: int(rawMajor), Minor: int(rawMinor)}, nil
}

// esp32s3ReadChipFeatures returns the chip feature list.
// Reference: esptool/targets/esp32s3.py get_chip_features().
func esp32s3ReadChipFeatures(f *Flasher) ([]string, error) {
	word3, err := f.ReadRegister(esp32s3EfuseBlock1Word3)
	if err != nil {
		return nil, err
	}
	word4, err := f.ReadRegister(esp32s3EfuseBlock1Word4)
	if err != nil {
		return nil, err
	}
	word5, err := f.ReadRegister(esp32s3EfuseBlock1Word5)
	if err != nil {
		return nil, err
	}

	features := []string{"Wi-Fi", "BT 5 (LE)", "Dual Core + LP Core", "240MHz"}

	flashCap := (word3 >> 27) & 0x7
	flash, ok := map[uint32]string{
		1: "Embedded Flash 8MB",
		2: "Embedded Flash 4MB",
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
			5: "BY",
		}[vendorID]
		features = append(features, fmt.Sprintf("%s (%s)", flash, vendor))
	}

	psramCap := ((word4 >> 3) & 0x3) | (((word5 >> 19) & 0x1) << 2)
	psram, ok := map[uint32]string{
		1: "Embedded PSRAM 8MB",
		2: "Embedded PSRAM 2MB",
		3: "Embedded PSRAM 16MB",
		4: "Embedded PSRAM 4MB",
	}[psramCap]
	if !ok && psramCap != 0 {
		psram = "Unknown Embedded PSRAM"
	}
	if psram != "" {
		vendorID := (word4 >> 7) & 0x3
		vendor := map[uint32]string{
			1: "AP_3v3",
			2: "AP_1v8",
		}[vendorID]
		features = append(features, fmt.Sprintf("%s (%s)", psram, vendor))
	}

	return features, nil
}
