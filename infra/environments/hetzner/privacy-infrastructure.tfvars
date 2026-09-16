# Durable #274 activation posture. Backup cleanup receives only the assumable
# dry-run identity here; exact-version deletion remains a later explicit gate.
privacy_restore_infrastructure_enabled     = true
privacy_restore_ledger_write_enabled       = true
privacy_restore_ledger_replay_enabled      = true
postgres_backup_cleanup_identity_enabled   = true
postgres_backup_noncurrent_cleanup_enabled = false
