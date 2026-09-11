//go:build !legacy_media_purge_execute

package main

// Destructive execution is absent from every ordinary build. A separately
// reviewed one-time artifact must opt in with the legacy_media_purge_execute
// build tag, and the storage operation still requires an approved inventory
// digest and the exact typed confirmation.
const legacyMediaPurgeExecutionEnabled = false
