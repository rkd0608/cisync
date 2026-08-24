package providers

import (
	"fmt"
)

// fakeExitError stands in for exec.ExitError in census tests.
type fakeExitError struct{}

func (e *fakeExitError) Error() string { return "exit status 1" }
func (e *fakeExitError) ExitCode() int { return 1 }
func (e *fakeExitError) String() string {
	return fmt.Sprintf("fake exit error: %s", e.Error())
}
