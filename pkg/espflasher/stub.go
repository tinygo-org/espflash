package espflasher

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

//go:embed stubs
var stubFS embed.FS

// chipStubName maps each supported chip type to its stub JSON filename stem.
var chipStubName = map[ChipType]string{
	ChipESP8266: "esp8266",
	ChipESP32:   "esp32",
	ChipESP32S2: "esp32s2",
	ChipESP32S3: "esp32s3",
	ChipESP32C2: "esp32c2",
	ChipESP32C3: "esp32c3",
	ChipESP32C5: "esp32c5",
	ChipESP32C6: "esp32c6",
	ChipESP32H2: "esp32h2",
}

// stubJSON mirrors the JSON structure of the esptool stub flasher files.
type stubJSON struct {
	Entry     uint32 `json:"entry"`
	Text      string `json:"text"`
	TextStart uint32 `json:"text_start"`
	Data      string `json:"data"`
	DataStart uint32 `json:"data_start"`
	BSSStart  uint32 `json:"bss_start"`
}

// stub holds the decoded stub loader image ready for uploading.
type stub struct {
	text      []byte
	textStart uint32
	data      []byte
	dataStart uint32
	entry     uint32
}

// stubFor returns the stub loader for the given chip type.
// Returns nil, false if no stub is available for the chip.
func stubFor(chipType ChipType) (*stub, bool) {
	name, ok := chipStubName[chipType]
	if !ok {
		return nil, false
	}

	raw, err := stubFS.ReadFile(fmt.Sprintf("stubs/%s.json", name))
	if err != nil {
		return nil, false
	}

	var sj stubJSON
	if err := json.Unmarshal(raw, &sj); err != nil {
		return nil, false
	}

	text, err := base64.StdEncoding.DecodeString(sj.Text)
	if err != nil {
		return nil, false
	}

	var data []byte
	if sj.Data != "" {
		data, err = base64.StdEncoding.DecodeString(sj.Data)
		if err != nil {
			return nil, false
		}
	}

	return &stub{
		text:      text,
		textStart: sj.TextStart,
		data:      data,
		dataStart: sj.DataStart,
		entry:     sj.Entry,
	}, true
}
