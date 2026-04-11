package espflasher

// ESP32-C6 register addresses for USB interface detection and watchdog control.
// Reference: esptool/targets/esp32c6.py
const (
	esp32c6UARTDevBufNo              uint32 = 0x4087F580 // ROM .bss: active console interface
	esp32c6UARTDevBufNoUSBJTAGSerial uint32 = 3          // USB-JTAG/Serial active

	esp32c6LPWDTConfig0     uint32 = 0x600B1C00
	esp32c6LPWDTWProtect    uint32 = 0x600B1C18
	esp32c6LPWDTSWDConf     uint32 = 0x600B1C1C
	esp32c6LPWDTSWDWProtect uint32 = 0x600B1C20
)

// ESP32-C6 target definition.
// Reference: https://github.com/espressif/esptool/blob/master/esptool/targets/esp32c6.py

var defESP32C6 = &chipDef{
	ChipType:       ChipESP32C6,
	Name:           "ESP32-C6",
	ImageChipID:    13,
	UsesMagicValue: false, // Uses chip ID

	SPIRegBase:  0x60003000,
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
		"80m": 0x0, // workaround for wrong mspi HS div value in ROM
		"40m": 0x0,
		"20m": 0x2,
	},

	FlashSizes: defaultFlashSizes(),

	PostConnect: esp32c6PostConnect,
}

// esp32c6PostConnect detects the USB interface type and disables watchdogs
// when connected via USB-JTAG/Serial. Without this, the LP WDT fires
// during flash and resets the chip mid-operation.
// Reference: esptool/targets/esp32c6.py _post_connect()
func esp32c6PostConnect(f *Flasher) error {
	uartDev, err := f.ReadRegister(esp32c6UARTDevBufNo)
	if err != nil {
		// In secure download mode, the register may be unreadable.
		// Default to non-USB behavior (safe fallback).
		return nil
	}

	if uartDev == esp32c6UARTDevBufNoUSBJTAGSerial {
		f.usesUSB = true
		f.logf("USB-JTAG/Serial interface detected, disabling watchdogs")
		return disableWatchdogsLP(f, esp32c6LPWDTConfig0, esp32c6LPWDTWProtect, esp32c6LPWDTSWDConf, esp32c6LPWDTSWDWProtect)
	}

	return nil
}
