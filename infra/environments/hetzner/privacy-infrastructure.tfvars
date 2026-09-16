# Durable #274 first stage. Provision restore infrastructure without caller,
# replay, backup-cleanup or deletion access. Protected workflows always load it.
privacy_restore_infrastructure_enabled     = true
privacy_restore_ledger_write_enabled       = false
privacy_restore_ledger_replay_enabled      = false
postgres_backup_cleanup_identity_enabled   = false
postgres_backup_noncurrent_cleanup_enabled = false
