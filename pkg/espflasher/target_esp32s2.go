package espflasher

// ESP32-S2 register addresses for USB interface detection.
// Reference: esptool/targets/esp32s2.py
const (
	esp32s2UARTDevBufNo       uint32 = 0x3FFFFD14 // ROM .bss: active console interface
	esp32s2UARTDevBufNoUSBOTG uint32 = 2          // USB-OTG active
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

	PostConnect: esp32s2PostConnect,
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
