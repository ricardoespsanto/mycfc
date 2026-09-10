-- name: BeginPrivacyUploadIntent :one
SELECT privacy_upload_begin(
 sqlc.arg(intent_id),sqlc.narg(subject_user_id),sqlc.arg(actor_user_id),sqlc.arg(source_kind),sqlc.arg(source_ref),
 sqlc.arg(service_code),sqlc.arg(content_type),sqlc.arg(size_bytes),sqlc.arg(hold_token)
) AS clock;

-- name: FinalizePrivacyUploadIntent :exec
SELECT privacy_upload_finalize(
 sqlc.arg(intent_id),sqlc.arg(hold_token),sqlc.arg(envelope_version),sqlc.arg(algorithm),sqlc.arg(encryption_key_id),
 sqlc.arg(encapsulation),sqlc.arg(nonce),sqlc.arg(ciphertext),sqlc.arg(digest_key_id),sqlc.arg(locator_digest),sqlc.arg(locator_commitment)
);

-- name: ConfirmPrivacyUploadPut :exec
SELECT privacy_upload_confirm_put(sqlc.arg(intent_id),sqlc.arg(hold_token));

-- name: MarkPrivacyUploadCleanup :exec
SELECT privacy_upload_mark_cleanup(sqlc.arg(intent_id),sqlc.arg(hold_token),sqlc.arg(reason_code));

-- name: AttachPrivacyUploadIntent :exec
SELECT privacy_upload_attach(
 sqlc.arg(intent_id),sqlc.arg(hold_token),sqlc.narg(prior_intent_id),sqlc.arg(source_kind),sqlc.arg(source_ref),
 sqlc.arg(object_key),sqlc.arg(content_type),sqlc.arg(size_bytes)
);

-- name: RemovePrivacyUploadIntent :exec
SELECT privacy_upload_remove(sqlc.arg(intent_id),sqlc.arg(actor_user_id),sqlc.arg(source_kind),sqlc.arg(source_ref));

-- name: CompletePrivacyUploadCleanup :exec
SELECT privacy_upload_cleanup_complete(
 sqlc.arg(intent_id),sqlc.arg(attempt_id),sqlc.arg(lease_epoch),sqlc.arg(worker_ref),
 sqlc.arg(deleted_versions),sqlc.arg(deleted_markers),sqlc.arg(list_calls),sqlc.arg(stable_checks),
 sqlc.arg(transcript_key_id),sqlc.arg(transcript_digest)
);

-- name: FailPrivacyUploadCleanup :exec
SELECT privacy_upload_cleanup_fail(
 sqlc.arg(intent_id),sqlc.arg(attempt_id),sqlc.arg(lease_epoch),sqlc.arg(worker_ref),
 sqlc.arg(retryable),sqlc.arg(retry_delay_milliseconds)
);
