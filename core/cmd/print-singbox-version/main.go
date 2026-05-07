// Command print-singbox-version prints the pinned sing-box version
// from the singbox package to stdout. Used by image/build.sh to keep
// the version source-of-truth in one place (singbox.Version) and avoid
// regex-parsing Go source from a shell script.
package main

import (
	"fmt"

	"github.com/knot-os/knot-os/core/internal/singbox"
)

func main() {
	fmt.Print(singbox.Version)
}
