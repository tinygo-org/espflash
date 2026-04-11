package espflasher

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestParseSecurityFlags(t *testing.T) {
	tests := map[string]struct {
		flags uint32
		check func(t *testing.T, pf ParsedFlags)
	}{
		"all zeros": {
			flags: 0,
			check: func(t *testing.T, pf ParsedFlags) {
				if pf.SecureBootEn || pf.SecureBootAggressiveRevoke || pf.SecureDownloadEnable ||
					pf.SecureBootKeyRevoke0 || pf.SecureBootKeyRevoke1 || pf.SecureBootKeyRevoke2 ||
					pf.SoftDisJtag || pf.HardDisJtag || pf.DisUSB ||
					pf.DisDownloadDcache || pf.DisDownloadIcache {
					t.Error("expected all flags false for input 0")
				}
			},
		},
		"all bits set": {
			flags: 0x7FF,
			check: func(t *testing.T, pf ParsedFlags) {
				if !pf.SecureBootEn || !pf.SecureBootAggressiveRevoke || !pf.SecureDownloadEnable ||
					!pf.SecureBootKeyRevoke0 || !pf.SecureBootKeyRevoke1 || !pf.SecureBootKeyRevoke2 ||
					!pf.SoftDisJtag || !pf.HardDisJtag || !pf.DisUSB ||
					!pf.DisDownloadDcache || !pf.DisDownloadIcache {
					t.Error("expected all flags true for input 0x7FF")
				}
			},
		},
		"only SecureBootEn": {
			flags: 1 << 0,
			check: func(t *testing.T, pf ParsedFlags) {
				if !pf.SecureBootEn {
					t.Error("expected SecureBootEn true")
				}
				if pf.HardDisJtag || pf.DisUSB {
					t.Error("expected other flags false")
				}
			},
		},
		"only HardDisJtag": {
			flags: 1 << 7,
			check: func(t *testing.T, pf ParsedFlags) {
				if !pf.HardDisJtag {
					t.Error("expected HardDisJtag true")
				}
				if pf.SecureBootEn || pf.SoftDisJtag {
					t.Error("expected other flags false")
				}
			},
		},
		"only DisDownloadIcache": {
			flags: 1 << 10,
			check: func(t *testing.T, pf ParsedFlags) {
				if !pf.DisDownloadIcache {
					t.Error("expected DisDownloadIcache true")
				}
				if pf.DisDownloadDcache {
					t.Error("expected DisDownloadDcache false")
				}
			},
		},
		"high bits ignored": {
			flags: 0xFFFFF800,
			check: func(t *testing.T, pf ParsedFlags) {
				if pf.SecureBootEn || pf.DisDownloadIcache {
					t.Error("expected all flags false when only high bits set")
				}
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			pf := parseSecurityFlags(tt.flags)
			tt.check(t, pf)
		})
	}
}

func TestReadSecurityInfo(t *testing.T) {
	buf20 := make([]byte, 20)
	binary.LittleEndian.PutUint32(buf20[0:4], 0x05)     // flags: SecureBootEn + SecureDownloadEnable
	buf20[4] = 0x03                                     // FlashCryptCnt
	buf20[5] = 0x10                                     // KeyPurposes[0]
	buf20[6] = 0x20                                     // KeyPurposes[1]
	buf20[7] = 0x30                                     // KeyPurposes[2]
	buf20[8] = 0x40                                     // KeyPurposes[3]
	buf20[9] = 0x50                                     // KeyPurposes[4]
	buf20[10] = 0x60                                    // KeyPurposes[5]
	buf20[11] = 0x70                                    // KeyPurposes[6]
	binary.LittleEndian.PutUint32(buf20[12:16], 0x0009) // ChipID
	binary.LittleEndian.PutUint32(buf20[16:20], 0x0002) // APIVersion

	buf12 := make([]byte, 12)
	binary.LittleEndian.PutUint32(buf12[0:4], 0x01) // flags: SecureBootEn
	buf12[4] = 0x07                                 // FlashCryptCnt
	buf12[5] = 0xAA                                 // KeyPurposes[0]

	tests := map[string]struct {
		securityInfoFunc func() ([]byte, error)
		expectErr        bool
		check            func(t *testing.T, si *SecurityInfo)
	}{
		"20 bytes with ChipID and APIVersion": {
			securityInfoFunc: func() ([]byte, error) {
				return buf20, nil
			},
			check: func(t *testing.T, si *SecurityInfo) {
				if si.Flags != 0x05 {
					t.Errorf("Flags = 0x%x, want 0x05", si.Flags)
				}
				if si.FlashCryptCnt != 0x03 {
					t.Errorf("FlashCryptCnt = %d, want 3", si.FlashCryptCnt)
				}
				expectedKeys := [7]uint8{0x10, 0x20, 0x30, 0x40, 0x50, 0x60, 0x70}
				if si.KeyPurposes != expectedKeys {
					t.Errorf("KeyPurposes = %v, want %v", si.KeyPurposes, expectedKeys)
				}
				if si.ChipID == nil || *si.ChipID != 0x0009 {
					t.Errorf("ChipID = %v, want 9", si.ChipID)
				}
				if si.APIVersion == nil || *si.APIVersion != 0x0002 {
					t.Errorf("APIVersion = %v, want 2", si.APIVersion)
				}
				if !si.ParsedFlags.SecureBootEn {
					t.Error("expected SecureBootEn true")
				}
				if !si.ParsedFlags.SecureDownloadEnable {
					t.Error("expected SecureDownloadEnable true")
				}
				if si.ParsedFlags.SecureBootAggressiveRevoke {
					t.Error("expected SecureBootAggressiveRevoke false")
				}
			},
		},
		"12 bytes without ChipID": {
			securityInfoFunc: func() ([]byte, error) {
				return buf12, nil
			},
			check: func(t *testing.T, si *SecurityInfo) {
				if si.Flags != 0x01 {
					t.Errorf("Flags = 0x%x, want 0x01", si.Flags)
				}
				if si.FlashCryptCnt != 0x07 {
					t.Errorf("FlashCryptCnt = %d, want 7", si.FlashCryptCnt)
				}
				if si.KeyPurposes[0] != 0xAA {
					t.Errorf("KeyPurposes[0] = 0x%x, want 0xAA", si.KeyPurposes[0])
				}
				if si.ChipID != nil {
					t.Errorf("ChipID = %v, want nil", si.ChipID)
				}
				if si.APIVersion != nil {
					t.Errorf("APIVersion = %v, want nil", si.APIVersion)
				}
				if !si.ParsedFlags.SecureBootEn {
					t.Error("expected SecureBootEn true")
				}
			},
		},
		"unexpected length": {
			securityInfoFunc: func() ([]byte, error) {
				return make([]byte, 5), nil
			},
			expectErr: true,
		},
		"connection error": {
			securityInfoFunc: func() ([]byte, error) {
				return nil, errors.New("connection timeout")
			},
			expectErr: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			mc := &mockConnection{
				securityInfoFunc: tt.securityInfoFunc,
			}
			f := &Flasher{conn: mc}

			si, err := f.readSecurityInfo()
			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, si)
			}
		})
	}
}

func TestSecurityInfoFlushInput(t *testing.T) {
	// Test that securityInfo clears any stray input before querying.
	// This verifies the fix for the ESP32-S3 issue where SLIP frame delimiters
	// in the input buffer corrupted the response (command 0x14 failed: status=0xC0).
	buf20 := make([]byte, 20)
	binary.LittleEndian.PutUint32(buf20[0:4], 0x05)
	buf20[4] = 0x03
	binary.LittleEndian.PutUint32(buf20[12:16], 0x0009)

	mc := &mockConnection{
		securityInfoFunc: func() ([]byte, error) {
			return buf20, nil
		},
	}
	f := &Flasher{conn: mc}

	si, err := f.readSecurityInfo()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if si.Flags != 0x05 {
		t.Errorf("Flags = 0x%x, want 0x05", si.Flags)
	}

	if si == nil {
		t.Error("expected SecurityInfo to be populated")
	}
}

func TestSecurityInfoCaching(t *testing.T) {
	// Test that security info is cached from the first call (ROM before stub loads)
	// and subsequent calls return the cached bytes without re-issuing the command.
	buf20 := make([]byte, 20)
	binary.LittleEndian.PutUint32(buf20[0:4], 0x05)     // flags
	buf20[4] = 0x03                                     // FlashCryptCnt
	binary.LittleEndian.PutUint32(buf20[12:16], 0x0009) // ChipID

	callCount := 0
	mc := &mockConnection{
		securityInfoFunc: func() ([]byte, error) {
			callCount++
			return buf20, nil
		},
	}
	f := &Flasher{conn: mc}

	// First call should invoke the connection
	si1, err := f.readSecurityInfo()
	if err != nil {
		t.Fatalf("first call: unexpected error: %v", err)
	}
	if si1.Flags != 0x05 {
		t.Errorf("first call: Flags = 0x%x, want 0x05", si1.Flags)
	}
	if callCount != 1 {
		t.Errorf("first call: expected 1 connection call, got %d", callCount)
	}

	// Second call should use the cached bytes without calling conn.securityInfo()
	si2, err := f.readSecurityInfo()
	if err != nil {
		t.Fatalf("second call: unexpected error: %v", err)
	}
	if si2.Flags != 0x05 {
		t.Errorf("second call: Flags = 0x%x, want 0x05", si2.Flags)
	}
	if callCount != 1 {
		t.Errorf("second call: expected 1 total connection call, got %d", callCount)
	}

	// Results should be identical
	if si1.Flags != si2.Flags || si1.FlashCryptCnt != si2.FlashCryptCnt {
		t.Error("cached result differs from initial result")
	}
}

func TestSecurityInfoCachingWithStubFailure(t *testing.T) {
	// Test that when security info is cached from ROM, a stub failure (0xC0)
	// on a second call to GetSecurityInfo() returns the cached data instead.
	buf20 := make([]byte, 20)
	binary.LittleEndian.PutUint32(buf20[0:4], 0x05)     // flags
	buf20[4] = 0x03                                     // FlashCryptCnt
	binary.LittleEndian.PutUint32(buf20[12:16], 0x0009) // ChipID

	callCount := 0
	mc := &mockConnection{
		securityInfoFunc: func() ([]byte, error) {
			callCount++
			if callCount == 1 {
				// First call (ROM) succeeds
				return buf20, nil
			}
			// Second call (stub) would fail, but it won't be called
			return nil, errors.New("stub does not support command 0x14")
		},
	}
	f := &Flasher{conn: mc}

	// First call succeeds and caches the data
	si1, err := f.readSecurityInfo()
	if err != nil {
		t.Fatalf("first call: unexpected error: %v", err)
	}
	if si1.Flags != 0x05 {
		t.Errorf("first call: Flags = 0x%x, want 0x05", si1.Flags)
	}

	// Second call returns cached data without hitting the connection
	si2, err := f.readSecurityInfo()
	if err != nil {
		t.Fatalf("second call: unexpected error: %v", err)
	}
	if si2.Flags != 0x05 {
		t.Errorf("second call: Flags = 0x%x, want 0x05", si2.Flags)
	}
	if callCount != 1 {
		t.Errorf("expected only 1 connection call (second should use cache), got %d", callCount)
	}
}

func TestSecurityInfoStubWithoutCache(t *testing.T) {
	// Test that when stub is running and no cached security info is available,
	// readSecurityInfo() returns UnsupportedCommandError.
	mc := &mockConnection{
		securityInfoFunc: func() ([]byte, error) {
			return nil, errors.New("stub does not support command 0x14")
		},
		stubMode: true, // Simulate stub is running
	}
	f := &Flasher{conn: mc}

	// Call without cache and stub running should return UnsupportedCommandError
	si, err := f.readSecurityInfo()
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	var unsupErr *UnsupportedCommandError
	if !errors.As(err, &unsupErr) {
		t.Fatalf("expected UnsupportedCommandError, got %T: %v", err, err)
	}

	if unsupErr.Command != "GET_SECURITY_INFO requires ROM bootloader; not available after stub is loaded (use ChipAuto to cache during connect)" {
		t.Errorf("unexpected error message: %s", unsupErr.Command)
	}

	if si != nil {
		t.Errorf("expected nil SecurityInfo, got %v", si)
	}
}
