package espflasher

// ESP32-P4 Rev1 (ECO2, chip revision < 3.0) register addresses for USB
// interface detection and watchdog control.
// Reference: esptool/targets/esp32p4.py (P4-specific values) and
// esptool/targets/esp32.py (ESP32ROM base-class SPI defaults P4 inherits).
//
// UARTDEV_BUF_NO is revision-dependent in esptool:
//   rev <  3.0 (ECO2, this target):  0x4FF3FEB0 + 24 = 0x4FF3FEC8
//   rev >= 3.0 (production, future): 0x4FFBFEB0 + 24 = 0x4FFBFEC8
// The USB-JTAG/Serial sentinel is 6 on P4 (not 3 like C5/C6/H2).
const (
	esp32p4Rev1UARTDevBufNo              uint32 = 0x4FF3FEC8
	esp32p4UARTDevBufNoUSBJTAGSerial     uint32 = 6

	esp32p4LPWDTConfig0     uint32 = 0x50116000
	esp32p4LPWDTWProtect    uint32 = 0x50116018
	esp32p4LPWDTSWDConf     uint32 = 0x5011601C
	esp32p4LPWDTSWDWProtect uint32 = 0x50116020
)

// ESP32-P4 Rev1 target definition.
// Reference: https://github.com/espressif/esptool/blob/master/esptool/targets/esp32p4.py
// Production silicon (rev >= 3.0) uses a different UARTDEV_BUF_NO address and
// the esp32p4.json stub; it will land as a separate target when we have that hardware.

var defESP32P4Rev1 = &chipDef{
	ChipType:       ChipESP32P4Rev1,
	Name:           "ESP32-P4-Rev1",
	ImageChipID:    18,
	UsesMagicValue: false,

	SPIRegBase:  0x5008D000,
	SPIUSROffs:  0x18,
	SPIUSR1Offs: 0x1C,
	SPIUSR2Offs: 0x20,
	SPIMOSIOffs: 0x24,
	SPIMISOOffs: 0x98,
	SPIW0Offs:   0x58,

	SPIMISODLenOffs: 0x28,
	SPIMOSIDLenOffs: 0x24,

	SPIAddrRegMSB: false,

	UARTDateReg: 0x500CA08C,
	UARTClkDiv:  0x500CA014,
	XTALClkDiv:  1,

	BootloaderFlashOffset: 0x2000,

	SupportsEncryptedFlash: true,
	ROMHasCompressedFlash:  true,
	ROMHasChangeBaud:       true,

	FlashFrequency: map[string]byte{
		"80m": 0xF,
		"40m": 0x0,
		"20m": 0x2,
	},

	FlashSizes: defaultFlashSizes(),

	PostConnect: esp32p4Rev1PostConnect,
}

func esp32p4Rev1PostConnect(f *Flasher) error {
	uartDev, err := f.ReadRegister(esp32p4Rev1UARTDevBufNo)
	if err != nil {
		return nil
	}

	if uartDev == esp32p4UARTDevBufNoUSBJTAGSerial {
		f.usesUSB = true
		f.logf("USB-JTAG/Serial interface detected (ESP32-P4 rev1), disabling watchdogs")
		return disableWatchdogsLP(f, esp32p4LPWDTConfig0, esp32p4LPWDTWProtect, esp32p4LPWDTSWDConf, esp32p4LPWDTSWDWProtect)
	}

	return nil
}
