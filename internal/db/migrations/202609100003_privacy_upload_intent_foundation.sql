-- Durable, protected upload/provenance states for the future v2 object
-- executor. No application or worker routine is granted by this migration.
CREATE TABLE privacy_protected.object_upload_intents (
 id uuid PRIMARY KEY, subject_user_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 provenance_actor_user_id uuid NULL REFERENCES users(id) ON DELETE RESTRICT,
 source_kind varchar(40) NOT NULL CHECK (source_kind IN ('MEMBER_PROFILE_PHOTO','REPAIR_ATTACHMENT','EQUIPMENT_PHOTO')),
 source_ref uuid NOT NULL, service_code varchar(120) NOT NULL,
 target_kind varchar(40) NOT NULL CHECK (target_kind='OBJECT_KEY'),
 provider_contract_version varchar(40) NOT NULL CHECK (provider_contract_version='s3-versioned/v1'),
 envelope_version varchar(80) NOT NULL CHECK (envelope_version='x25519-aes256gcm-hkdfsha256/upload-intent-v1'),
 algorithm varchar(80) NOT NULL CHECK (algorithm='X25519-HKDF-SHA256-AES-256-GCM'),
 encryption_key_id varchar(80) NOT NULL, encapsulation bytea NOT NULL CHECK (octet_length(encapsulation)=32),
 nonce bytea NOT NULL CHECK (octet_length(nonce)=12), ciphertext bytea NOT NULL CHECK (octet_length(ciphertext) BETWEEN 17 AND 2048),
 digest_key_id varchar(80) NOT NULL, locator_digest bytea NOT NULL CHECK (octet_length(locator_digest)=32),
 content_type varchar(100) NOT NULL CHECK (content_type IN ('image/jpeg','image/png','image/webp')),
 size_bytes bigint NOT NULL CHECK (size_bytes BETWEEN 1 AND 10485760), cleanup_after timestamptz NOT NULL, created_at timestamptz NOT NULL,
 CHECK (service_code=btrim(service_code) AND service_code ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,119}$'),
 CHECK (encryption_key_id=btrim(encryption_key_id) AND encryption_key_id ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 CHECK (digest_key_id=btrim(digest_key_id) AND digest_key_id ~ '^[A-Za-z0-9][A-Za-z0-9_.:/-]{0,79}$'),
 CHECK ((source_kind IN ('MEMBER_PROFILE_PHOTO','REPAIR_ATTACHMENT') AND subject_user_id IS NOT NULL) OR (source_kind='EQUIPMENT_PHOTO' AND subject_user_id IS NULL)),
 CHECK (cleanup_after>created_at), UNIQUE(digest_key_id,locator_digest), UNIQUE(encryption_key_id,encapsulation)
);
CREATE TABLE privacy_protected.object_upload_intent_events (
 intent_id uuid NOT NULL REFERENCES privacy_protected.object_upload_intents(id) ON DELETE RESTRICT,
 sequence integer NOT NULL CHECK (sequence > 0),
 status varchar(30) NOT NULL CHECK (status IN ('PREPARED','PUT_CONFIRMED','ATTACHED','CLEANUP_REQUIRED','ABSENCE_VERIFIED')),
 reason_code varchar(40) NOT NULL CHECK (reason_code IN ('UPLOAD_RESERVED','PUT_ACKNOWLEDGED','POINTER_ATTACHED','PUT_AMBIGUOUS','PUT_FAILED','ATTACH_FAILED','POINTER_SUPERSEDED','POINTER_REMOVED','CLEANUP_CONFIRMED')),
 occurred_at timestamptz NOT NULL,
 CHECK ((status='PREPARED' AND reason_code='UPLOAD_RESERVED')
     OR (status='PUT_CONFIRMED' AND reason_code='PUT_ACKNOWLEDGED')
     OR (status='ATTACHED' AND reason_code='POINTER_ATTACHED')
     OR (status='CLEANUP_REQUIRED' AND reason_code IN ('PUT_AMBIGUOUS','PUT_FAILED','ATTACH_FAILED','POINTER_SUPERSEDED','POINTER_REMOVED'))
     OR (status='ABSENCE_VERIFIED' AND reason_code='CLEANUP_CONFIRMED')),
 PRIMARY KEY(intent_id,sequence)
);

ALTER TABLE member_profiles ADD COLUMN photo_upload_intent_id uuid NULL;
ALTER TABLE repair_requests ADD COLUMN image_upload_intent_id uuid NULL;
ALTER TABLE equipment ADD COLUMN image_upload_intent_id uuid NULL;
ALTER TABLE member_profiles ADD CONSTRAINT member_profiles_photo_upload_intent_fk FOREIGN KEY(photo_upload_intent_id) REFERENCES privacy_protected.object_upload_intents(id) ON DELETE RESTRICT;
ALTER TABLE repair_requests ADD CONSTRAINT repair_requests_image_upload_intent_fk FOREIGN KEY(image_upload_intent_id) REFERENCES privacy_protected.object_upload_intents(id) ON DELETE RESTRICT;
ALTER TABLE equipment ADD CONSTRAINT equipment_image_upload_intent_fk FOREIGN KEY(image_upload_intent_id) REFERENCES privacy_protected.object_upload_intents(id) ON DELETE RESTRICT;

CREATE TRIGGER privacy_object_upload_intents_immutable BEFORE UPDATE OR DELETE ON privacy_protected.object_upload_intents FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();
CREATE TRIGGER privacy_object_upload_intent_events_immutable BEFORE UPDATE OR DELETE ON privacy_protected.object_upload_intent_events FOR EACH ROW EXECUTE FUNCTION prevent_privacy_execution_record_delete();

REVOKE ALL ON TABLE privacy_protected.object_upload_intents,privacy_protected.object_upload_intent_events FROM PUBLIC;
