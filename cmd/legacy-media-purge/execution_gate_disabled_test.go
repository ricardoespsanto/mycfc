//go:build !legacy_media_purge_execute

package main

import "testing"

func TestLegacyMediaPurgeOrdinaryBuildDisablesExecution(t *testing.T) {
	if legacyMediaPurgeExecutionEnabled {
		t.Fatal("ordinary build enabled destructive legacy media execution")
	}
}
