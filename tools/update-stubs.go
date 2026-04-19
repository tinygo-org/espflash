// Command update-stubs downloads pre-compiled bootloader stubs from the
// esp-flasher-stub GitHub releases and writes them to pkg/espflasher/stubs/.
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

const (
	stubVersion = "v0.5.1"
	baseURL     = "https://github.com/espressif/esp-flasher-stub/releases/download/" + stubVersion
)

var chips = []string{
	"esp8266",
	"esp32",
	"esp32s2",
	"esp32s3",
	"esp32c2",
	"esp32c3",
	"esp32c5",
	"esp32c6",
	"esp32h2",
	"esp32p4-rev1",
}

func main() {
	// Resolve the stubs directory relative to this tool's source file.
	stubDir := filepath.Join("..", "pkg", "espflasher", "stubs")
	if dir := os.Getenv("STUB_DIR"); dir != "" {
		stubDir = dir
	}

	fmt.Printf("Downloading stubs from %s to %s\n", baseURL, stubDir)

	for _, chip := range chips {
		url := baseURL + "/" + chip + ".json"
		dest := filepath.Join(stubDir, chip+".json")

		if err := download(url, dest); err != nil {
			fmt.Fprintf(os.Stderr, "error downloading %s: %v\n", chip, err)
			os.Exit(1)
		}
	}

	fmt.Printf("Successfully updated all %d stubs to %s\n", len(chips), stubVersion)
}

func download(url, dest string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s for %s", resp.Status, url)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer func() {
		_ = f.Close()
	}()

	_, err = io.Copy(f, resp.Body)
	return err
}
