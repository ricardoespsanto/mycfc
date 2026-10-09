package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrClassificationStale = errors.New("classification changed; reload and review")

type classificationVersionReader interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// SportingClassificationVersion covers both axes and immutable event identities: removal of
// Canoagem cascades crafts, and an intervening change then reversal is still stale.
func SportingClassificationVersion(ctx context.Context, q classificationVersionReader, member uuid.UUID) (string, error) {
	return classificationVersion(ctx, q, member, `SELECT jsonb_build_array(
 (SELECT COALESCE(jsonb_agg(modality_code ORDER BY modality_code),'[]') FROM person_sporting_modalities WHERE user_id=$1),
 (SELECT COALESCE(jsonb_agg(craft_code ORDER BY craft_code),'[]') FROM person_canoe_craft_classes WHERE user_id=$1),
 (SELECT COALESCE(jsonb_agg(id ORDER BY id),'[]') FROM person_sport_assignment_events WHERE subject_user_id=$1))::text`)
}

// DatedClassificationVersion excludes private exception reasons. Definitions and the automatically
// resolved team are part of the preview contract as well as interval identities.
func DatedClassificationVersion(ctx context.Context, q classificationVersionReader, member uuid.UUID) (string, error) {
	return classificationVersion(ctx, q, member, `SELECT jsonb_build_array(
 (SELECT COALESCE(jsonb_agg(jsonb_build_array(id,season_id,programme_id,team_id,competition_category_id,starts_on,ends_on,updated_at) ORDER BY id),'[]') FROM user_memberships WHERE user_id=$1),
 (SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY s.id),'[]') FROM seasons s WHERE is_current),
 (SELECT COALESCE(jsonb_agg(to_jsonb(c) ORDER BY c.id),'[]') FROM competition_categories c JOIN seasons s ON s.id=c.season_id WHERE s.is_current),
 (SELECT COALESCE(jsonb_agg(jsonb_build_array(t.id,t.name,t.programme_id,t.season_id) ORDER BY t.id),'[]') FROM teams t JOIN seasons s ON s.id=t.season_id WHERE s.is_current))::text`)
}

func classificationVersion(ctx context.Context, q classificationVersionReader, member uuid.UUID, sql string) (string, error) {
	var value string
	if err := q.QueryRow(ctx, sql, member).Scan(&value); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:]), nil
}
