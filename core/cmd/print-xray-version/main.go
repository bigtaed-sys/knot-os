// Command print-xray-version prints the pinned Xray-core version
// from the xray package to stdout. Used by image/build.sh to keep
// the version source-of-truth in one place (xray.Version) and avoid
// regex-parsing Go source from a shell script.
package main

import (
	"fmt"

	"github.com/knot-os/knot-os/core/internal/xray"
)

func main() {
	fmt.Print(xray.Version)
}
