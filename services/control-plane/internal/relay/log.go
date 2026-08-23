package relay

import (
	"fmt"
	"os"
)

var osStderr = os.Stderr

func logf(format string, args ...any) {
	fmt.Fprintf(osStderr, "control-plane/relay: "+format+"\n", args...)
}
