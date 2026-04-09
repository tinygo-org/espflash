package nvs

// NVS v2 format constants
const (
	PageSize           = 4096
	HeaderSize         = 32
	BitmapSize         = 32
	EntrySize          = 32
	EntriesPerPage     = 126
	FirstEntryOffset   = 64 // HeaderSize + BitmapSize
	DefaultPages       = 6
	DefaultPartSize    = PageSize * DefaultPages // 0x6000

	pageStateActive  = 0xFE
	pageStateEmpty   = 0xFF
	pageVersion      = 0xFE // v2

	maxKeyLen        = 15
	namespaceType    = 0x01
	typeU8           = 0x01
	typeU16          = 0x02
	typeI8           = 0x11
	typeI16          = 0x12
	typeU32          = 0x04
	typeI32          = 0x14
	typeString       = 0x21 // SZ (null-terminated)
	typeBlob         = 0x41

	singleChunkIndex = 0xFF
	spanOne          = 1

	entryStateEmpty   = 0x03 // 0b11
	entryStateWritten = 0x02 // 0b10
	entryStateErased  = 0x00 // 0b00
)

// Entry represents an NVS key-value pair with its namespace.
type Entry struct {
	Namespace string
	Key       string
	Type      string      // "u8", "u16", "u32", "i8", "i16", "i32", "string", "blob"
	Value     interface{}
}
