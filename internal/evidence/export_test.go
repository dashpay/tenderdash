package evidence

import "time"

// SetEvidenceSyncIntervalForTesting overrides evidenceSyncInterval for the
// duration of a test. Call the returned function (typically via defer) to
// restore the original value.
//
// This is an internal-package test export compiled only during `go test`.
// It is not part of the package's public API.
func SetEvidenceSyncIntervalForTesting(d time.Duration) func() {
	old := evidenceSyncInterval
	evidenceSyncInterval = d
	return func() { evidenceSyncInterval = old }
}
