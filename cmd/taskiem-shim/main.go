// Command taskiem-shim supervises a container step's program inside the
// container sandbox (docs/container-steps.md): an init container installs
// it into a shared volume, and the step's container runs the program under
// it. It reports the output, logs and exit status as one line on stdout,
// which the worker reads from the Pod's log.
package main

import (
	"fmt"
	"os"

	"github.com/israel-duff/taskiem/engine/container"
)

func main() {
	if err := container.Shim(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "taskiem-shim:", err)
		os.Exit(2)
	}
}
