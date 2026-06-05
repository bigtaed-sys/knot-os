package zapret

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// seedAssets carries the first-run defaults: domain hostlists and the
// fake-payload .bin blobs. These are written to disk only when the
// target file is ABSENT — so an updated list the user refreshed from
// upstream survives a knotd binary update.
//
//go:embed assets
var seedAssets embed.FS

// MaterializeAssets seeds the on-disk asset tree under base. .txt files
// land in lists/, .bin files in bin/. Existing files are left intact
// (they may be newer than the embedded seed). Returns the number of
// files written.
func MaterializeAssets(base string) (int, error) {
	written := 0
	entries, err := fs.ReadDir(seedAssets, "assets")
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(ListsDir(base), 0o755); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(FakeDir(base), 0o755); err != nil {
		return 0, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		var dst string
		switch {
		case strings.HasSuffix(name, ".txt"):
			dst = filepath.Join(ListsDir(base), name)
		case strings.HasSuffix(name, ".bin"):
			dst = filepath.Join(FakeDir(base), name)
		default:
			continue
		}
		if _, err := os.Stat(dst); err == nil {
			continue // already present — don't clobber updated copies
		}
		data, err := seedAssets.ReadFile("assets/" + name)
		if err != nil {
			return written, err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return written, fmt.Errorf("seed %s: %w", dst, err)
		}
		written++
	}
	return written, nil
}
