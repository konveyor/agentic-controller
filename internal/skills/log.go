package skills

import (
	"fmt"
	"os"
)

// logf reports progress from the loader, which runs as a one-shot container
// whose stderr is what an operator reads with `kubectl logs`. Deliberately not
// the controller's logr: nothing here reconciles, and a structured logger
// would bury a one-line explanation of which skill was wrong.
func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}
