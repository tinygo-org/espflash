package espflash

import (
"bytes"
"testing"
)

func TestSlipEncode(t *testing.T) {
tests := []struct {
name     string
input    []byte
expected []byte
}{
{
name:     "empty data",
input:    []byte{},
expected: []byte{0xC0, 0xC0},
},
{
name:     "no special bytes",
input:    []byte{0x01, 0x02, 0x03},
expected: []byte{0xC0, 0x01, 0x02, 0x03, 0xC0},
},
{
name:     "escape END byte",
input:    []byte{0x01, 0xC0, 0x03},
expected: []byte{0xC0, 0x01, 0xDB, 0xDC, 0x03, 0xC0},
},
{
name:     "escape ESC byte",
input:    []byte{0x01, 0xDB, 0x03},
expected: []byte{0xC0, 0x01, 0xDB, 0xDD, 0x03, 0xC0},
},
{
name:     "escape both special bytes",
input:    []byte{0xC0, 0xDB},
expected: []byte{0xC0, 0xDB, 0xDC, 0xDB, 0xDD, 0xC0},
},
{
name:     "multiple END bytes",
input:    []byte{0xC0, 0xC0, 0xC0},
expected: []byte{0xC0, 0xDB, 0xDC, 0xDB, 0xDC, 0xDB, 0xDC, 0xC0},
},
{
name:     "multiple ESC bytes",
input:    []byte{0xDB, 0xDB},
expected: []byte{0xC0, 0xDB, 0xDD, 0xDB, 0xDD, 0xC0},
},
{
name:     "adjacent ESC and END",
input:    []byte{0xDB, 0xC0},
expected: []byte{0xC0, 0xDB, 0xDD, 0xDB, 0xDC, 0xC0},
},
}

for _, tt := range tests {
t.Run(tt.name, func(t *testing.T) {
result := slipEncode(tt.input)
if !bytes.Equal(result, tt.expected) {
t.Errorf("slipEncode(%X) = %X, want %X", tt.input, result, tt.expected)
}
})
}
}

func TestSlipDecode(t *testing.T) {
tests := []struct {
name     string
input    []byte
expected []byte
}{
{
name:     "no special bytes",
input:    []byte{0xC0, 0x01, 0x02, 0x03, 0xC0},
expected: []byte{0x01, 0x02, 0x03},
},
{
name:     "unescape END byte",
input:    []byte{0xC0, 0x01, 0xDB, 0xDC, 0x03, 0xC0},
expected: []byte{0x01, 0xC0, 0x03},
},
{
name:     "unescape ESC byte",
input:    []byte{0xC0, 0x01, 0xDB, 0xDD, 0x03, 0xC0},
expected: []byte{0x01, 0xDB, 0x03},
},
{
name:     "unescape both",
input:    []byte{0xDB, 0xDC, 0xDB, 0xDD},
expected: []byte{0xC0, 0xDB},
},
{
name:     "empty frame",
input:    []byte{0xC0, 0xC0},
expected: []byte{},
},
{
name:     "frame delimiters stripped",
input:    []byte{0xC0, 0x41, 0x42, 0xC0},
expected: []byte{0x41, 0x42},
},
{
name:     "invalid escape preserved",
input:    []byte{0xDB, 0x42},
expected: []byte{0xDB, 0x42},
},
}

for _, tt := range tests {
t.Run(tt.name, func(t *testing.T) {
result := slipDecode(tt.input)
if !bytes.Equal(result, tt.expected) {
t.Errorf("slipDecode(%X) = %X, want %X", tt.input, result, tt.expected)
}
})
}
}

func TestSlipRoundtrip(t *testing.T) {
// Encoding then decoding should give back the original data.
testCases := [][]byte{
{},
{0x00},
{0xFF},
{0xC0},
{0xDB},
{0xC0, 0xDB},
{0x01, 0x02, 0x03, 0x04, 0x05},
{0xC0, 0xDB, 0xC0, 0xDB, 0xC0},
bytes.Repeat([]byte{0xC0}, 10),
bytes.Repeat([]byte{0xDB}, 10),
}

for _, data := range testCases {
encoded := slipEncode(data)
// Verify frame delimiters
if encoded[0] != slipEnd || encoded[len(encoded)-1] != slipEnd {
t.Errorf("slipEncode(%X): missing frame delimiters", data)
}
decoded := slipDecode(encoded)
if !bytes.Equal(decoded, data) {
t.Errorf("roundtrip failed for %X: got %X", data, decoded)
}
}
}

func TestSlipEncodeNoRawSpecialBytes(t *testing.T) {
// Verify that encoded data (between delimiters) never contains raw 0xC0.
data := []byte{0x00, 0xC0, 0xDB, 0xFF, 0xC0, 0xDB, 0x42}
encoded := slipEncode(data)
inner := encoded[1 : len(encoded)-1] // strip delimiters
for i, b := range inner {
if b == slipEnd {
t.Errorf("raw 0xC0 found at inner byte %d in encoded data %X", i, encoded)
}
}
}
