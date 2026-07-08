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
	Type      string      // "u8", "u16", "u32", "i8", "i16", "i32", "string", "blob", or "raw"
	Value     interface{}

	// Raw entries are captured via generic passthrough when ParseNVS encounters
	// a type byte it does not natively decode (future/vendor NVS types, or the
	// blob-index/blob-data entries ESP-IDF uses for chunked values such as
	// esp_wifi credentials). When Raw is true, GenerateNVS ignores Type/Value
	// and re-emits the slot(s) byte-for-byte from TypeByte/Span/ChunkIndex/Data
	// so a read-modify-write round trip never drops or corrupts data it does
	// not understand.
	Raw        bool
	TypeByte   uint8  // raw NVS entry-type byte (entryBytes[1])
	Span       uint8  // number of 32-byte slots this entry occupies, header included
	ChunkIndex uint8  // NVS chunk index; 0xFF (singleChunkIndex) means "not chunked"
	Data       []byte // raw payload: first 8 bytes are the entry header's "data" field
	// (entryBytes[24:32]); any remaining bytes are the verbatim continuation
	// slot(s) content, length exactly (Span-1)*EntrySize.
}
