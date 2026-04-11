package espflasher

import "fmt"

// ESP32-S3 register addresses for USB interface detection and watchdog control.
// Reference: esptool/targets/esp32s3.py
const (
	esp32s3UARTDevBufNo              uint32 = 0x3FCEF14C // ROM .bss: active console interface
	esp32s3UARTDevBufNoUSBOTG        uint32 = 3          // USB-OTG (CDC) active
	esp32s3UARTDevBufNoUSBJTAGSerial uint32 = 4          // USB-JTAG/Serial active

	esp32s3RTCCntlWDTConfig0  uint32 = 0x60008098
	esp32s3RTCCntlWDTWProtect uint32 = 0x600080B0
	esp32s3RTCCntlWDTWKey     uint32 = 0x50D83AA1

	esp32s3RTCCntlSWDConf       uint32 = 0x600080B4
	esp32s3RTCCntlSWDAutoFeedEn uint32 = 1 << 31
	esp32s3RTCCntlSWDWProtect   uint32 = 0x600080B8
	esp32s3RTCCntlSWDWKey       uint32 = 0x8F1D312A
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
}

// esp32s3PostConnect detects the USB interface type and disables watchdogs
// when connected via USB-JTAG/Serial. Without this, the RTC WDT fires
// during flash and resets the chip mid-operation.
// Reference: esptool/targets/esp32s3.py _post_connect()
func esp32s3PostConnect(f *Flasher) error {
	val, err := f.conn.readReg(esp32s3UARTDevBufNo)
	if err != nil {
		// In secure download mode, the register may be unreadable.
		// Default to non-USB behavior (safe fallback).
		return nil
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
