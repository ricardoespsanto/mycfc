package db

// ClassificationSubjectEligibilitySQL is the existing dated writer's registered
// subject policy. The caller must alias users as member; no membership anchor is
// needed for name-only selection or first assignment. Guardian details are never
// projected. Keep search, detail and transactional writes on this same predicate.
const ClassificationSubjectEligibilitySQL = `member.is_active AND member.erased_at IS NULL AND (NOT member.is_dependent OR EXISTS (
 SELECT 1 FROM guardian_authority_relationships r WHERE r.subject_user_id=member.id AND guardian_authority_current(r.guardian_user_id,member.id)))`
