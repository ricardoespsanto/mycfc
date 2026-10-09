-- The legacy modalities table remains the read model for dated training/events and
-- already-published prescriptions. These queries only write the new vocabulary.

-- name: ListSportingModalities :many
SELECT code,name_pt FROM sporting_modalities ORDER BY code;

-- name: ListCanoeCraftClasses :many
SELECT code,name_pt FROM canoe_craft_classes ORDER BY code;

-- name: ListPersonSportingModalities :many
SELECT sport.code,sport.name_pt
FROM person_sporting_modalities assignment
JOIN sporting_modalities sport ON sport.code=assignment.modality_code
WHERE assignment.user_id=sqlc.arg(user_id)::uuid
ORDER BY sport.code;

-- name: ListPersonSportAssignmentEvents :many
SELECT id,operation_id,subject_user_id,actor_user_id,coach_grant_id,kind,action,code,reason,occurred_at
FROM person_sport_assignment_events
WHERE subject_user_id=sqlc.arg(user_id)::uuid
ORDER BY occurred_at DESC,id DESC;

-- name: AssignScopedSportingModality :execrows
INSERT INTO person_sporting_modalities(user_id,modality_code)
SELECT subject.id, sport.code
FROM users subject CROSS JOIN sporting_modalities sport
JOIN users actor ON actor.id=sqlc.arg(actor_id)::uuid AND actor.is_active
WHERE subject.id=sqlc.arg(user_id)::uuid AND subject.is_active AND subject.erased_at IS NULL
 AND sport.code=sqlc.arg(modality_code)
 AND (
  EXISTS (SELECT 1 FROM user_platform_roles r JOIN platform_roles role ON role.id=r.role_id
   WHERE r.user_id=actor.id AND role.code='ADMIN')
  OR EXISTS (SELECT 1 FROM user_memberships membership
    JOIN staff_grants grant_row ON grant_row.user_id=actor.id AND grant_row.capability='COACH'
      AND grant_row.revoked_at IS NULL
      AND (grant_row.programme_id=membership.programme_id OR grant_row.team_id=membership.team_id)
    WHERE membership.user_id=subject.id AND membership.starts_on<=CURRENT_DATE
      AND membership.ends_on>=CURRENT_DATE)
 )
ON CONFLICT DO NOTHING;

-- name: AssignScopedCanoeCraftClass :execrows
INSERT INTO person_canoe_craft_classes(user_id,modality_code,craft_code)
SELECT assignment.user_id,'CANOEING',craft.code
FROM person_sporting_modalities assignment
JOIN canoe_craft_classes craft ON craft.code=sqlc.arg(craft_code)
JOIN users actor ON actor.id=sqlc.arg(actor_id)::uuid AND actor.is_active
WHERE assignment.user_id=sqlc.arg(user_id)::uuid AND assignment.modality_code='CANOEING'
 AND (
  EXISTS (SELECT 1 FROM user_platform_roles r JOIN platform_roles role ON role.id=r.role_id
   WHERE r.user_id=actor.id AND role.code='ADMIN')
  OR EXISTS (SELECT 1 FROM user_memberships membership
    JOIN staff_grants grant_row ON grant_row.user_id=actor.id AND grant_row.capability='COACH'
      AND grant_row.revoked_at IS NULL
      AND (grant_row.programme_id=membership.programme_id OR grant_row.team_id=membership.team_id)
    WHERE membership.user_id=assignment.user_id AND membership.starts_on<=CURRENT_DATE
      AND membership.ends_on>=CURRENT_DATE)
 )
ON CONFLICT DO NOTHING;
