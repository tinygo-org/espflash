package nvs

import (
	"encoding/binary"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test fixtures in this file hand-build raw NVS page bytes (header + entry
// state bitmap + 32-byte entry slots) using the package's own internal
// primitives (newEntry, writeEntry, writePageHeader, markBitmapWritten,
// calculateEntryCRC32, parseEntry, writePage) so the fixtures stay realistic
// without duplicating CRC/layout logic. They exercise read-modify-write
// scenarios the original round-trip tests couldn't reach: namespace
// declarations scanned after their keys, unmodeled type bytes, chunked
// blob-index values (esp_wifi credentials), an item bumped whole onto a
// fresh page when it doesn't fit the current one, generator determinism,
// and a full lossless RMW that must leave everything but the modified key
// byte-for-byte intact.
//
// Every fixture here models a layout that ESP-IDF's on-flash format can
// actually produce: an item's header slot and all of its continuation slots
// always live on the SAME page (see Page::writeItem in nvs_page.cpp).
// Nothing here hand-builds an item whose span straddles a page boundary —
// that layout cannot exist on real hardware.

// newTestPage returns a fresh, correctly-headered, all-0xFF NVS page.
func newTestPage(seqNum uint32) []byte {
	page := make([]byte, PageSize)
	for i := range page {
		page[i] = 0xFF
	}
	writePageHeader(page, seqNum)
	return page
}

// placeEntry writes an entry's 32-byte header slot at slotIdx and marks it
// written in the bitmap. It does not write any continuation slots.
func placeEntry(page []byte, slotIdx int, e *entry) {
	markBitmapWritten(page, HeaderSize, slotIdx)
	off := FirstEntryOffset + slotIdx*EntrySize
	writeEntry(page[off:off+EntrySize], e)
}

// placeContinuation writes a raw 32-byte continuation slot (payload padded
// with 0xFF, matching how writePage lays out span data) and marks it written.
func placeContinuation(page []byte, slotIdx int, payload []byte) {
	markBitmapWritten(page, HeaderSize, slotIdx)
	off := FirstEntryOffset + slotIdx*EntrySize
	for i := range EntrySize {
		page[off+i] = 0xFF
	}
	copy(page[off:off+EntrySize], payload)
}

// nsEntry builds a namespace-declaration entry (index -> name).
func nsEntry(name string, idx uint8) *entry {
	e := newEntry()
	e.namespaceIdx = 0
	e.entryType = namespaceType
	e.span = spanOne
	e.chunkIndex = singleChunkIndex
	copyKeyToEntry(name, e)
	e.data[0] = idx
	e.crc32Val = calculateEntryCRC32(e)
	return e
}

// u8Entry builds a single-slot u8-typed entry.
func u8Entry(nsIdx uint8, key string, val uint8) *entry {
	e := newEntry()
	e.namespaceIdx = nsIdx
	e.entryType = typeU8
	e.span = spanOne
	e.chunkIndex = singleChunkIndex
	copyKeyToEntry(key, e)
	e.data[0] = val
	e.crc32Val = calculateEntryCRC32(e)
	return e
}

// assertNoItemCrossesPage scans a generated partition and fails the test if
// any item's header + continuation slots (per its span byte) would run past
// the end of its page's EntriesPerPage slots. Per ESP-IDF's Page::writeItem,
// that layout can never occur on real hardware; GenerateNVS must never
// produce it either.
func assertNoItemCrossesPage(t *testing.T, partition []byte) {
	t.Helper()
	totalPages := len(partition) / PageSize
	for p := 0; p < totalPages; p++ {
		page := partition[p*PageSize : (p+1)*PageSize]
		if page[0] == pageStateEmpty {
			continue
		}
		slotIdx := 0
		for slotIdx < EntriesPerPage {
			bitIndex := uint(slotIdx) * 2
			byteIdx := HeaderSize + int(bitIndex/8)
			bitOffset := bitIndex % 8
			state := (page[byteIdx] >> bitOffset) & 0x3
			if state != entryStateWritten {
				slotIdx++
				continue
			}
			off := FirstEntryOffset + slotIdx*EntrySize
			span := page[off+2]
			if span == 0 {
				span = 1
			}
			require.LessOrEqualf(t, slotIdx+int(span), EntriesPerPage,
				"page %d slot %d: item span %d crosses the page boundary (EntriesPerPage=%d)",
				p, slotIdx, span, EntriesPerPage)
			slotIdx += int(span)
		}
	}
}

// pagesContainingHeaderSlot returns, for each valid page in partition, the
// slot index of a written header slot matching (key, typeByte, chunkIndex),
// keyed by page number. Used to assert *where* GenerateNVS physically placed
// an item.
func pagesContainingHeaderSlot(partition []byte, key string, typeByte uint8, chunkIndex uint8) map[int]int {
	found := make(map[int]int)
	totalPages := len(partition) / PageSize
	for p := 0; p < totalPages; p++ {
		page := partition[p*PageSize : (p+1)*PageSize]
		if page[0] == pageStateEmpty {
			continue
		}
		for slotIdx := 0; slotIdx < EntriesPerPage; slotIdx++ {
			bitIndex := uint(slotIdx) * 2
			byteIdx := HeaderSize + int(bitIndex/8)
			bitOffset := bitIndex % 8
			state := (page[byteIdx] >> bitOffset) & 0x3
			if state != entryStateWritten {
				continue
			}
			off := FirstEntryOffset + slotIdx*EntrySize
			entryBytes := page[off : off+EntrySize]
			if entryBytes[1] != typeByte || entryBytes[3] != chunkIndex {
				continue
			}
			if readNullTerminatedString(entryBytes[8:24]) != key {
				continue
			}
			found[p] = slotIdx
		}
	}
	return found
}

// 1. Namespace declared on a page scanned later than the key referencing it
// (a lower sequence-number page can still be scanned after a higher one if
// physical order doesn't match seq order; here we keep it simple with
// physical order == seq order, but resolution is scan-order independent).
// The old single-pass scan dropped such keys ("namespace not yet defined,
// skip"); the two-phase parse must still resolve them.
func TestParseNVSNamespaceDeclaredOnLaterPage(t *testing.T) {
	partition := make([]byte, PageSize*2)

	page0 := newTestPage(0)
	placeEntry(page0, 0, u8Entry(1, "channel", 6)) // references ns index 1, not yet declared
	copy(partition[0:PageSize], page0)

	page1 := newTestPage(1)
	placeEntry(page1, 0, nsEntry("wifi", 1)) // declared here, on a later page
	copy(partition[PageSize:2*PageSize], page1)

	entries, err := ParseNVS(partition)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	assert.Equal(t, "wifi", entries[0].Namespace)
	assert.Equal(t, "channel", entries[0].Key)
	assert.Equal(t, uint8(6), entries[0].Value)
}

// 2. An entry with a type byte the switch doesn't model must be captured via
// generic passthrough, not dropped, and GenerateNVS must re-emit an
// equivalent slot.
func TestParseNVSUnknownTypePassthrough(t *testing.T) {
	page := newTestPage(0)
	placeEntry(page, 0, nsEntry("cfg", 1))

	unk := newEntry()
	unk.namespaceIdx = 1
	unk.entryType = 0x99 // not modeled by the codec
	unk.span = spanOne
	unk.chunkIndex = singleChunkIndex
	copyKeyToEntry("future", unk)
	unk.data = [8]byte{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x03, 0x04}
	unk.crc32Val = calculateEntryCRC32(unk)
	placeEntry(page, 1, unk)

	partition := make([]byte, PageSize)
	copy(partition, page)

	entries, err := ParseNVS(partition)
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.True(t, entries[0].Raw)
	assert.Equal(t, uint8(0x99), entries[0].TypeByte)
	assert.Equal(t, "future", entries[0].Key)
	assert.Equal(t, unk.data[:], entries[0].Data)

	regen, err := GenerateNVS(entries, DefaultPartSize)
	require.NoError(t, err)
	reparsed, err := ParseNVS(regen)
	require.NoError(t, err)
	require.Len(t, reparsed, 1)
	assert.Equal(t, entries[0].Namespace, reparsed[0].Namespace)
	assert.Equal(t, entries[0].Key, reparsed[0].Key)
	assert.Equal(t, entries[0].TypeByte, reparsed[0].TypeByte)
	assert.Equal(t, entries[0].ChunkIndex, reparsed[0].ChunkIndex)
	assert.Equal(t, entries[0].Data, reparsed[0].Data)
}

// 3. Blob-index + chunked blob-data entries (as ESP-IDF stores esp_wifi
// credentials): an index entry plus N data-chunk entries sharing one key but
// distinguished by chunkIndex, each a self-contained (single-page) item. All
// chunks must survive parse and a parse->generate->parse round trip, not be
// collapsed by key-only deduplication.
func TestParseNVSBlobIndexChunkedBlobRoundTrip(t *testing.T) {
	const typeBlobIdx = 0x48
	const typeBlobData = 0x42

	page := newTestPage(0)
	placeEntry(page, 0, nsEntry("wifi", 1))

	idx := newEntry()
	idx.namespaceIdx = 1
	idx.entryType = typeBlobIdx
	idx.span = spanOne
	idx.chunkIndex = singleChunkIndex
	copyKeyToEntry("creds", idx)
	idx.data = [8]byte{16, 0, 2, 0, 0xFF, 0xFF, 0xFF, 0xFF} // size=16, chunkCount=2
	idx.crc32Val = calculateEntryCRC32(idx)
	placeEntry(page, 1, idx)

	chunk0 := newEntry()
	chunk0.namespaceIdx = 1
	chunk0.entryType = typeBlobData
	chunk0.span = 2
	chunk0.chunkIndex = 0
	copyKeyToEntry("creds", chunk0)
	chunk0.data = [8]byte{8, 0, 0xFF, 0xFF, 0, 0, 0, 0}
	chunk0.crc32Val = calculateEntryCRC32(chunk0)
	placeEntry(page, 2, chunk0)
	payload0 := make([]byte, EntrySize)
	for i := range payload0 {
		payload0[i] = 0xFF
	}
	copy(payload0, []byte("PASSWORD"))
	placeContinuation(page, 3, payload0)

	chunk1 := newEntry()
	chunk1.namespaceIdx = 1
	chunk1.entryType = typeBlobData
	chunk1.span = 2
	chunk1.chunkIndex = 1
	copyKeyToEntry("creds", chunk1)
	chunk1.data = [8]byte{8, 0, 0xFF, 0xFF, 0, 0, 0, 0}
	chunk1.crc32Val = calculateEntryCRC32(chunk1)
	placeEntry(page, 4, chunk1)
	payload1 := make([]byte, EntrySize)
	for i := range payload1 {
		payload1[i] = 0xFF
	}
	copy(payload1, []byte("SSID1234"))
	placeContinuation(page, 5, payload1)

	partition := make([]byte, PageSize)
	copy(partition, page)

	entries, err := ParseNVS(partition)
	require.NoError(t, err)
	require.Len(t, entries, 3)

	byChunk := make(map[uint8]Entry)
	for _, e := range entries {
		require.True(t, e.Raw)
		require.Equal(t, "creds", e.Key)
		byChunk[e.ChunkIndex] = e
	}
	require.Contains(t, byChunk, uint8(singleChunkIndex))
	require.Contains(t, byChunk, uint8(0))
	require.Contains(t, byChunk, uint8(1))
	assert.Equal(t, append(append([]byte{}, chunk0.data[:]...), payload0...), byChunk[0].Data)
	assert.Equal(t, append(append([]byte{}, chunk1.data[:]...), payload1...), byChunk[1].Data)

	regen, err := GenerateNVS(entries, DefaultPartSize)
	require.NoError(t, err)
	assertNoItemCrossesPage(t, regen)
	reparsed, err := ParseNVS(regen)
	require.NoError(t, err)
	require.Len(t, reparsed, 3)

	byChunk2 := make(map[uint8]Entry)
	for _, e := range reparsed {
		byChunk2[e.ChunkIndex] = e
	}
	assert.Equal(t, byChunk[singleChunkIndex].Data, byChunk2[singleChunkIndex].Data)
	assert.Equal(t, byChunk[0].Data, byChunk2[0].Data)
	assert.Equal(t, byChunk[1].Data, byChunk2[1].Data)
}

// 4a. A large chunked blob (BLOB_IDX + BLOB_DATA chunks, as ESP-IDF actually
// stores values too big for one item's span) where the chunks are large
// enough that they cannot both fit on the same page: GenerateNVS must place
// each chunk as a whole, self-contained item — never split one across pages
// — landing chunk 1 on a different physical page than chunk 0. This is the
// real-hardware equivalent of what the old (removed) "blob crosses page
// boundary" fixture tried to model with an impossible single-item span.
func TestParseNVSLargeBlobChunksSpanMultiplePages(t *testing.T) {
	const typeBlobIdx = 0x48
	const typeBlobData = 0x42

	// Each chunk needs 1 (header) + 110 (data) = 111 slots. Two chunks plus
	// the namespace + index entries (2 more slots) can't both fit in a
	// single 126-slot page, so chunk 1 must roll onto a fresh page.
	const chunkPayloadLen = 110 * EntrySize // 3520 bytes
	const chunkSlots = 1 + chunkPayloadLen/EntrySize

	payload0 := make([]byte, chunkPayloadLen)
	for i := range payload0 {
		payload0[i] = byte(i)
	}
	payload1 := make([]byte, chunkPayloadLen)
	for i := range payload1 {
		payload1[i] = byte(i + 1)
	}

	chunkHeader := func(payloadLen int) []byte {
		h := make([]byte, 8)
		binary.LittleEndian.PutUint16(h[0:2], uint16(payloadLen))
		for i := 2; i < 8; i++ {
			h[i] = 0xFF
		}
		return h
	}

	idxData := make([]byte, 8)
	binary.LittleEndian.PutUint16(idxData[0:2], uint16(2*chunkPayloadLen))
	idxData[2] = 2 // chunk count
	for i := 3; i < 8; i++ {
		idxData[i] = 0xFF
	}

	entries := []Entry{
		{
			Namespace: "wifi", Key: "creds", Raw: true,
			TypeByte: typeBlobIdx, Span: spanOne, ChunkIndex: singleChunkIndex,
			Data: idxData,
		},
		{
			Namespace: "wifi", Key: "creds", Raw: true,
			TypeByte: typeBlobData, Span: uint8(chunkSlots), ChunkIndex: 0,
			Data: append(append([]byte{}, chunkHeader(chunkPayloadLen)...), payload0...),
		},
		{
			Namespace: "wifi", Key: "creds", Raw: true,
			TypeByte: typeBlobData, Span: uint8(chunkSlots), ChunkIndex: 1,
			Data: append(append([]byte{}, chunkHeader(chunkPayloadLen)...), payload1...),
		},
	}

	regen, err := GenerateNVS(entries, PageSize*3)
	require.NoError(t, err)
	assertNoItemCrossesPage(t, regen)

	locs := pagesContainingHeaderSlot(regen, "creds", typeBlobData, 0)
	require.Len(t, locs, 1, "chunk 0 should land on exactly one page")
	var chunk0Page int
	for p := range locs {
		chunk0Page = p
	}
	locs1 := pagesContainingHeaderSlot(regen, "creds", typeBlobData, 1)
	require.Len(t, locs1, 1, "chunk 1 should land on exactly one page")
	var chunk1Page int
	for p := range locs1 {
		chunk1Page = p
	}
	assert.NotEqual(t, chunk0Page, chunk1Page, "chunk 0 and chunk 1 should not fit on the same page and must land on different pages")

	reparsed, err := ParseNVS(regen)
	require.NoError(t, err)
	require.Len(t, reparsed, 3)

	byChunk := make(map[uint8]Entry)
	for _, e := range reparsed {
		require.True(t, e.Raw)
		require.Equal(t, "creds", e.Key)
		byChunk[e.ChunkIndex] = e
	}
	require.Contains(t, byChunk, uint8(singleChunkIndex))
	require.Contains(t, byChunk, uint8(0))
	require.Contains(t, byChunk, uint8(1))
	assert.Equal(t, entries[1].Data, byChunk[0].Data)
	assert.Equal(t, entries[2].Data, byChunk[1].Data)
}

// 4b. An item that does not fit in the current page's remaining tail slots
// must be placed WHOLE on a fresh page — never split — leaving the tail of
// the previous page Empty. Fill page 0 with a namespace declaration plus 123
// single-slot u8 entries (124 slots used, 2 free), then add a 5-slot string
// entry that can't fit in those 2 remaining slots.
func TestGenerateNVSBumpsOversizedTailItemToNextPage(t *testing.T) {
	var entries []Entry
	for i := 0; i < 123; i++ {
		entries = append(entries, Entry{
			Namespace: "cfg", Key: fmt.Sprintf("k%d", i), Type: "u8", Value: uint8(i % 256),
		})
	}
	// strLen=100 -> ceil(101/32)=4 data slots + 1 header = 5 slots; page 0 has
	// only 2 slots left (126 - 1 ns - 123 u8 = 2), so this must move whole to
	// page 1.
	longStr := make([]byte, 100)
	for i := range longStr {
		longStr[i] = byte('a' + i%26)
	}
	entries = append(entries, Entry{Namespace: "cfg", Key: "biggie", Type: "string", Value: string(longStr)})

	partition, err := GenerateNVS(entries, PageSize*2)
	require.NoError(t, err)
	assertNoItemCrossesPage(t, partition)

	// Page 0's tail (slots 124, 125) must be Empty, not holding part of
	// "biggie".
	page0 := partition[0:PageSize]
	for _, slotIdx := range []int{124, 125} {
		bitIndex := uint(slotIdx) * 2
		byteIdx := HeaderSize + int(bitIndex/8)
		bitOffset := bitIndex % 8
		state := (page0[byteIdx] >> bitOffset) & 0x3
		assert.Equal(t, uint8(entryStateEmpty), state, "page 0 slot %d should be left Empty", slotIdx)
	}

	// "biggie" must be found whole on page 1.
	locs := pagesContainingHeaderSlot(partition, "biggie", typeString, singleChunkIndex)
	require.Len(t, locs, 1)
	for p := range locs {
		assert.Equal(t, 1, p, "biggie should have been bumped whole onto page 1")
	}

	// Everything parses back, including every u8 filler key and the moved
	// string — nothing on page 1 is skipped or lost.
	parsed, err := ParseNVS(partition)
	require.NoError(t, err)
	require.Len(t, parsed, 124) // 123 u8 fillers + biggie

	parsedMap := make(map[string]Entry)
	for _, e := range parsed {
		parsedMap[e.Key] = e
	}
	require.Contains(t, parsedMap, "biggie")
	assert.Equal(t, string(longStr), parsedMap["biggie"].Value)
	for i := 0; i < 123; i++ {
		key := fmt.Sprintf("k%d", i)
		require.Contains(t, parsedMap, key)
		assert.Equal(t, uint8(i%256), parsedMap[key].Value)
	}
}

// 4c. Page sequence-number reordering: ParseNVS must scan pages in ascending
// header-seqNum order, not physical/flash order (see the sort.SliceStable in
// ParseNVS). Build a 2-page partition where the SAME (namespace, key) is
// written on both pages with different values, and vary which physical page
// carries the higher sequence number. Write chronology (seqNum), never
// physical placement, must decide which value survives dedup.
func TestParseNVSHigherSeqWinsWhenPhysicalOrderInverted(t *testing.T) {
	buildPartition := func(page0Seq, page1Seq uint32, page0Val, page1Val uint8) []byte {
		partition := make([]byte, PageSize*2)

		page0 := newTestPage(page0Seq)
		placeEntry(page0, 0, nsEntry("cfg", 1))
		placeEntry(page0, 1, u8Entry(1, "value", page0Val))
		copy(partition[0:PageSize], page0)

		page1 := newTestPage(page1Seq)
		placeEntry(page1, 0, u8Entry(1, "value", page1Val))
		copy(partition[PageSize:2*PageSize], page1)

		return partition
	}

	t.Run("physical order inverted relative to seq order", func(t *testing.T) {
		// Page 0 (lower physical offset) has the HIGHER seq number, so
		// physical order [page0, page1] is the reverse of seq order
		// [page1, page0]. If ParseNVS scanned physical order instead of
		// seq order, or the sort comparator got inverted, page1's value
		// (the older write) would incorrectly win.
		partition := buildPartition(5, 2, 99, 11)

		entries, err := ParseNVS(partition)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "cfg", entries[0].Namespace)
		assert.Equal(t, "value", entries[0].Key)
		assert.Equal(t, uint8(99), entries[0].Value, "higher-seq page (page 0) should win despite lower physical offset")
	})

	t.Run("physical order matches seq order", func(t *testing.T) {
		// Page 1 (higher physical offset) now holds the higher seq
		// number, matching physical order. The higher-seq value must
		// still win, proving the outcome is keyed on seqNum and not on
		// physical page index in either direction.
		partition := buildPartition(2, 5, 11, 99)

		entries, err := ParseNVS(partition)
		require.NoError(t, err)
		require.Len(t, entries, 1)
		assert.Equal(t, "cfg", entries[0].Namespace)
		assert.Equal(t, "value", entries[0].Key)
		assert.Equal(t, uint8(99), entries[0].Value, "higher-seq page (page 1) should win")
	})
}

// 5. GenerateNVS must produce identical bytes across repeated calls with the
// same input; Go map iteration order previously made this flaky/nondeterministic.
func TestGenerateNVSDeterministic(t *testing.T) {
	entries := []Entry{
		{Namespace: "wifi", Key: "ssid", Type: "string", Value: "Net"},
		{Namespace: "pool", Key: "host", Type: "string", Value: "pool.example.com"},
		{Namespace: "cfg", Key: "flag", Type: "u8", Value: uint8(1)},
		{Namespace: "misc", Key: "x", Type: "u32", Value: uint32(7)},
	}

	first, err := GenerateNVS(entries, DefaultPartSize)
	require.NoError(t, err)
	assertNoItemCrossesPage(t, first)

	for i := 0; i < 10; i++ {
		again, err := GenerateNVS(entries, DefaultPartSize)
		require.NoError(t, err)
		assert.Equal(t, first, again, "iteration %d differed", i)
	}
}

// 6. Full lossless RMW: parse a realistic multi-namespace image (primitives +
// a chunked blob-index value + a page-contained blob), modify exactly one
// key, regenerate, and reparse. The modified key must reflect the change and
// every other entry must be byte-preserved. Every item in this fixture is
// page-contained, per ESP-IDF's on-flash format.
func TestFullLosslessRoundTripModifyOneKey(t *testing.T) {
	const nsCfg, nsWifi = uint8(1), uint8(2)

	var allEntries []*entry
	allEntries = append(allEntries, nsEntry("cfg", nsCfg), nsEntry("wifi", nsWifi))

	flagEnts, err := parseEntry(&Entry{Namespace: "cfg", Key: "flag", Type: "u8", Value: uint8(1)}, nsCfg)
	require.NoError(t, err)
	allEntries = append(allEntries, flagEnts...)

	nameEnts, err := parseEntry(&Entry{Namespace: "cfg", Key: "name", Type: "string", Value: "device"}, nsCfg)
	require.NoError(t, err)
	allEntries = append(allEntries, nameEnts...)

	// A blob large enough to require several continuation slots but still
	// comfortably page-contained (1 header + 25 data slots = 26 of 126).
	bigBlob := make([]byte, 800)
	for i := range bigBlob {
		bigBlob[i] = byte(i)
	}
	blobEnts, err := parseEntry(&Entry{Namespace: "wifi", Key: "bigblob", Type: "blob", Value: bigBlob}, nsWifi)
	require.NoError(t, err)
	allEntries = append(allEntries, blobEnts...)

	idx := newEntry()
	idx.namespaceIdx = nsWifi
	idx.entryType = 0x48
	idx.span = spanOne
	idx.chunkIndex = singleChunkIndex
	copyKeyToEntry("creds", idx)
	idx.data = [8]byte{16, 0, 2, 0, 0xFF, 0xFF, 0xFF, 0xFF}
	idx.crc32Val = calculateEntryCRC32(idx)
	allEntries = append(allEntries, idx)

	chunk0 := newEntry()
	chunk0.namespaceIdx = nsWifi
	chunk0.entryType = 0x42
	chunk0.span = 2
	chunk0.chunkIndex = 0
	copyKeyToEntry("creds", chunk0)
	chunk0.data = [8]byte{8, 0, 0xFF, 0xFF, 0, 0, 0, 0}
	chunk0.crc32Val = calculateEntryCRC32(chunk0)
	chunk0.rawData = []byte("PASSWORD")
	allEntries = append(allEntries, chunk0)

	chunk1 := newEntry()
	chunk1.namespaceIdx = nsWifi
	chunk1.entryType = 0x42
	chunk1.span = 2
	chunk1.chunkIndex = 1
	copyKeyToEntry("creds", chunk1)
	chunk1.data = [8]byte{8, 0, 0xFF, 0xFF, 0, 0, 0, 0}
	chunk1.crc32Val = calculateEntryCRC32(chunk1)
	chunk1.rawData = []byte("SSID1234")
	allEntries = append(allEntries, chunk1)

	const totalPages = 6
	partition := make([]byte, PageSize*totalPages)
	for i := range partition {
		partition[i] = 0xFF
	}
	_, err = writePage(&partition, 0, 0, allEntries, totalPages)
	require.NoError(t, err)
	assertNoItemCrossesPage(t, partition)

	parsed, err := ParseNVS(partition)
	require.NoError(t, err)
	require.Len(t, parsed, 6) // flag, name, bigblob, creds(idx), creds(chunk0), creds(chunk1)

	// Snapshot originals for later comparison.
	type key struct {
		ns, k string
		chunk uint8
	}
	originals := make(map[key]Entry)
	for _, e := range parsed {
		originals[key{e.Namespace, e.Key, e.ChunkIndex}] = e
	}

	// Modify exactly one key.
	modified := make([]Entry, len(parsed))
	copy(modified, parsed)
	found := false
	for i := range modified {
		if modified[i].Namespace == "cfg" && modified[i].Key == "flag" {
			modified[i].Value = uint8(2)
			found = true
		}
	}
	require.True(t, found, "did not find cfg:flag to modify")

	regen, err := GenerateNVS(modified, PageSize*totalPages)
	require.NoError(t, err)
	assertNoItemCrossesPage(t, regen)
	reparsed, err := ParseNVS(regen)
	require.NoError(t, err)
	require.Len(t, reparsed, 6)

	reparsedMap := make(map[key]Entry)
	for _, e := range reparsed {
		reparsedMap[key{e.Namespace, e.Key, e.ChunkIndex}] = e
	}

	// The modified key changed.
	flag, ok := reparsedMap[key{"cfg", "flag", singleChunkIndex}]
	require.True(t, ok)
	assert.Equal(t, uint8(2), flag.Value)

	// Everything else is byte-preserved.
	for k, orig := range originals {
		if k.ns == "cfg" && k.k == "flag" {
			continue
		}
		got, ok := reparsedMap[k]
		require.True(t, ok, "entry %+v missing after round trip", k)
		if orig.Raw {
			assert.True(t, got.Raw, "%+v: expected Raw", k)
			assert.Equal(t, orig.TypeByte, got.TypeByte, "%+v: TypeByte", k)
			assert.Equal(t, orig.Span, got.Span, "%+v: Span", k)
			assert.Equal(t, orig.ChunkIndex, got.ChunkIndex, "%+v: ChunkIndex", k)
			assert.Equal(t, orig.Data, got.Data, "%+v: Data", k)
		} else {
			assert.Equal(t, orig.Type, got.Type, "%+v: Type", k)
			assert.Equal(t, orig.Value, got.Value, "%+v: Value", k)
		}
	}
}

// 7. Real-world regression fixture: testdata/real_espidf_nvs.bin is a 24576
// byte (6 page) NVS v2 partition produced by ESP-IDF's own
// nvs_partition_gen.py (not hand-built by this package), containing three
// namespaces — wifi_cfg (string ssid/pass, u8 channel/provisioned), app_cfg
// (string hostname, u32 counter), and blob_ns holding a single chunked blob
// ("bigblob") too large for one item: a BLOB_IDX header entry plus two
// BLOB_DATA chunks (chunk 0: 3616 payload bytes, chunk 1: 2528 payload bytes,
// 6144 bytes total). This locks the codec against a real esp-idf image
// instead of only synthetic fixtures.
func TestParseNVSRealESPIDFBlobPartitionRoundTrips(t *testing.T) {
	data, err := os.ReadFile("testdata/real_espidf_nvs.bin")
	require.NoError(t, err)
	require.Len(t, data, DefaultPartSize)

	entries, err := ParseNVS(data)
	require.NoError(t, err)
	require.Len(t, entries, 9)

	byNSKey := make(map[string]Entry)
	rawByChunk := make(map[uint8]Entry)
	for _, e := range entries {
		if e.Namespace == "blob_ns" && e.Key == "bigblob" {
			require.True(t, e.Raw, "blob_ns:bigblob chunk %d should be captured via raw passthrough", e.ChunkIndex)
			rawByChunk[e.ChunkIndex] = e
			continue
		}
		byNSKey[e.Namespace+"/"+e.Key] = e
	}

	assert.Equal(t, "MyHomeNetwork", byNSKey["wifi_cfg/ssid"].Value)
	assert.Equal(t, "SuperSecretPassphrase123", byNSKey["wifi_cfg/pass"].Value)
	assert.Equal(t, uint8(6), byNSKey["wifi_cfg/channel"].Value)
	assert.Equal(t, uint8(1), byNSKey["wifi_cfg/provisioned"].Value)
	assert.Equal(t, "esp32-device-01", byNSKey["app_cfg/hostname"].Value)
	assert.Equal(t, uint32(424242), byNSKey["app_cfg/counter"].Value)

	// BLOB_IDX header (chunkIndex singleChunkIndex) plus two BLOB_DATA chunks.
	require.Contains(t, rawByChunk, uint8(singleChunkIndex))
	require.Contains(t, rawByChunk, uint8(0))
	require.Contains(t, rawByChunk, uint8(1))
	assert.Equal(t, uint8(0x48), rawByChunk[singleChunkIndex].TypeByte)
	assert.Equal(t, uint8(0x42), rawByChunk[0].TypeByte)
	assert.Equal(t, uint8(0x42), rawByChunk[1].TypeByte)

	// Each chunk's Data is an 8-byte item header followed by its verbatim
	// continuation-slot payload; the blob's total content length is the sum
	// of the two chunks' payloads (Data minus the 8-byte header each).
	chunk0Payload := len(rawByChunk[0].Data) - 8
	chunk1Payload := len(rawByChunk[1].Data) - 8
	assert.Equal(t, 3616, chunk0Payload)
	assert.Equal(t, 2528, chunk1Payload)
	assert.Equal(t, 6144, chunk0Payload+chunk1Payload)

	// Round trip: regenerate the parsed model and reparse; every namespace,
	// key, value, and raw blob chunk must come back byte-identical.
	regen, err := GenerateNVS(entries, DefaultPartSize)
	require.NoError(t, err)
	assertNoItemCrossesPage(t, regen)

	reparsed, err := ParseNVS(regen)
	require.NoError(t, err)
	require.Len(t, reparsed, 9)

	type key struct {
		ns, k string
		chunk uint8
	}
	origByKey := make(map[key]Entry)
	for _, e := range entries {
		origByKey[key{e.Namespace, e.Key, e.ChunkIndex}] = e
	}
	for _, got := range reparsed {
		orig, ok := origByKey[key{got.Namespace, got.Key, got.ChunkIndex}]
		require.True(t, ok, "entry %s/%s chunk %d missing from original parse", got.Namespace, got.Key, got.ChunkIndex)
		if orig.Raw {
			assert.True(t, got.Raw, "%s/%s: expected Raw", got.Namespace, got.Key)
			assert.Equal(t, orig.TypeByte, got.TypeByte, "%s/%s: TypeByte", got.Namespace, got.Key)
			assert.Equal(t, orig.Span, got.Span, "%s/%s: Span", got.Namespace, got.Key)
			assert.Equal(t, orig.Data, got.Data, "%s/%s: Data", got.Namespace, got.Key)
		} else {
			assert.Equal(t, orig.Type, got.Type, "%s/%s: Type", got.Namespace, got.Key)
			assert.Equal(t, orig.Value, got.Value, "%s/%s: Value", got.Namespace, got.Key)
		}
	}
}
