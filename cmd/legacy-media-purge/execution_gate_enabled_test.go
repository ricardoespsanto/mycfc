//go:build legacy_media_purge_execute

package main

import "testing"

func TestLegacyMediaPurgeOneTimeBuildEnablesExecution(t *testing.T) {
	if !legacyMediaPurgeExecutionEnabled {
		t.Fatal("one-time build did not enable legacy media execution")
	}
}
