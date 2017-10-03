package embed

import (
	"os/exec"

	"github.com/DataDog/datadog-agent/pkg/collector/check"
)

// retryExitError converts `exec.ExitError`s to `check.RetryableError`s, so that checks using this
// are retried.
// Most embed checks should use this in their `Run` method to return an error type
// that the runner can work with.
func retryExitError(err error) error {
	switch err.(type) {
	case *exec.ExitError:
		return check.RetryableError{err}
	default:
		return err
	}
}
