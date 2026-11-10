-- #395 optional setup facts. Each read repeats exact current group authority;
-- no roster, subject, grant identity, or unrelated planner data is selected.

-- name: TrainingGroupSetupStatus :one
WITH actor AS (
 SELECT u.id, EXISTS (
  SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id
  WHERE r.user_id=u.id AND p.code='ADMIN'
 ) AS administrator
 FROM users u
 WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL
  AND NOT u.is_dependent
  AND u.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
)
SELECT programme.code::text AS programme_code, actor.administrator,
 (actor.administrator OR EXISTS (
  SELECT 1 FROM staff_grants grant_row
  WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH'
   AND grant_row.revoked_at IS NULL AND grant_row.programme_id=programme.id
 ))::boolean AS programme_authority,
 NOT EXISTS (SELECT 1 FROM seasons season WHERE season.is_current) AS missing_season,
 (programme.code IN ('Competition','Initiation') AND EXISTS (
  SELECT 1 FROM competition_categories category
  JOIN seasons season ON season.id=category.season_id AND season.is_current
  WHERE category.programme_id=programme.id
 ))::boolean AS category_configured
FROM training_groups group_row
LEFT JOIN teams team ON team.id=group_row.team_id
JOIN programmes programme ON programme.id=COALESCE(group_row.programme_id,team.programme_id)
CROSS JOIN actor
WHERE group_row.id=sqlc.arg(group_id)::uuid
 AND (actor.administrator OR EXISTS (
  SELECT 1 FROM staff_grants grant_row
  WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH'
   AND grant_row.revoked_at IS NULL
   AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id)
    OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))
 ));

-- name: TrainingGroupSetupWeek :one
WITH actor AS (
 SELECT u.id, EXISTS (
  SELECT 1 FROM user_platform_roles r JOIN platform_roles p ON p.id=r.role_id
  WHERE r.user_id=u.id AND p.code='ADMIN'
 ) AS administrator
 FROM users u
 WHERE u.id=sqlc.arg(actor_id)::uuid AND u.is_active AND u.erased_at IS NULL
  AND NOT u.is_dependent
  AND u.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date
)
SELECT plan.week_start
FROM training_plans plan
JOIN training_groups group_row ON group_row.id=plan.training_group_id
CROSS JOIN actor
WHERE group_row.id=sqlc.arg(group_id)::uuid AND plan.id=sqlc.arg(week_id)::uuid
 AND (actor.administrator OR EXISTS (
  SELECT 1 FROM staff_grants grant_row
  WHERE grant_row.user_id=actor.id AND grant_row.capability='COACH'
   AND grant_row.revoked_at IS NULL
   AND ((group_row.programme_id IS NOT NULL AND grant_row.programme_id=group_row.programme_id)
    OR (group_row.team_id IS NOT NULL AND grant_row.team_id=group_row.team_id))
 ));
