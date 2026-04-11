package espflasher

// disableWatchdogsLP disables LP domain watchdogs on RISC-V chips (C6, H2, C5).
// Register addresses vary per chip but the protocol is identical:
// unlock WDT write-protect, disable WDT, re-lock, then unlock SWD, enable auto-feed, re-lock.
func disableWatchdogsLP(f *Flasher, wdtConfig0, wdtWProtect, swdConf, swdWProtect uint32) error {
	const (
		lpWDTWKey       uint32 = 0x50D83AA1
		lpSWDAutoFeedEn uint32 = 1 << 18
	)

	// Unlock and disable RTC/LP WDT
	if err := f.WriteRegister(wdtWProtect, lpWDTWKey); err != nil {
		return err
	}
	if err := f.WriteRegister(wdtConfig0, 0); err != nil {
		return err
	}
	if err := f.WriteRegister(wdtWProtect, 0); err != nil {
		return err
	}

	// Unlock SWD, enable auto-feed, re-lock
	if err := f.WriteRegister(swdWProtect, lpWDTWKey); err != nil {
		return err
	}
	val, err := f.ReadRegister(swdConf)
	if err != nil {
		return err
	}
	if err := f.WriteRegister(swdConf, val|lpSWDAutoFeedEn); err != nil {
		return err
	}
	return f.WriteRegister(swdWProtect, 0)
}
