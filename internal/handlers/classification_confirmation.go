package handlers

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"strconv"
	"strings"
	"time"
)

// Process-local signing key: restarting invalidates outstanding previews safely.
// Tokens contain only opaque version/binding hashes and expiry, never reasons.
var classificationConfirmationKey = func() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return key
}()

func confirmationBinding(actor, member uuid.UUID, kind string, value any) string {
	b, _ := json.Marshal(value)
	sum := sha256.Sum256(append([]byte(actor.String()+":"+member.String()+":"+kind+":"), b...))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
func signClassificationConfirmation(binding, version string, now time.Time) string {
	payload := binding + "." + version + "." + strconv.FormatInt(now.Add(15*time.Minute).Unix(), 10)
	mac := hmac.New(sha256.New, classificationConfirmationKey)
	mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func verifyClassificationConfirmation(token, binding string, now time.Time) (string, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != binding {
		return "", false
	}
	expiry, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || now.Unix() >= expiry || expiry > now.Add(15*time.Minute).Unix() {
		return "", false
	}
	mac := hmac.New(sha256.New, classificationConfirmationKey)
	mac.Write([]byte(strings.Join(parts[:3], ".")))
	sig, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return "", false
	}
	return parts[1], true
}

// Version hashes are protected personal metadata too. Each individual version
// SELECT repeats current subject/actor eligibility, not merely the prior View.
type classificationScopedVersionReader struct {
	pool  *pgxpool.Pool
	actor uuid.UUID
}

func (q classificationScopedVersionReader) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	scope := strings.ReplaceAll(classificationFirstScope, "$1", "$2")
	return q.pool.QueryRow(ctx, sql+` WHERE EXISTS(SELECT 1 FROM users member WHERE member.id=$1 AND `+db.ClassificationSubjectEligibilitySQL+` AND `+scope+`)`, append(args, q.actor)...)
}

func (s PostgresClassificationStore) Versions(ctx context.Context, actor, member uuid.UUID) (string, string, error) {
	if _, _, err := s.View(ctx, actor, member); err != nil {
		return "", "", err
	}
	reader := classificationScopedVersionReader{pool: s.Pool, actor: actor}
	sport, err := db.SportingClassificationVersion(ctx, reader, member)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrClassificationDenied
		}
		return "", "", err
	}
	dated, err := db.DatedClassificationVersion(ctx, reader, member)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrClassificationDenied
		}
		return "", "", err
	}
	return sport, dated, nil
}

func (h Classification) datedPreview(ctx context.Context, actor, member uuid.UUID, options []ClassificationOption, f *classificationForm, today time.Time) error {
	_, rows, err := h.Store.View(ctx, actor, member)
	if err != nil {
		return err
	}
	f.PreviewOld = "Sem intervalo anterior a encerrar."
	if f.PreviousID != "" {
		found := false
		for _, row := range rows {
			if row.ID == f.PreviousID && row.StartsOn < today.Format("2006-01-02") && (row.EndsOn == "" || row.EndsOn >= f.StartsOn) {
				category := row.Category
				if category == "" {
					category = "sem escalão"
				}
				team := row.Team
				if team == "" {
					team = "sem equipa"
				}
				end := row.EndsOn
				if end == "" {
					end = "sem data de fim"
				}
				f.PreviewOld = row.Programme + " — escalão: " + category + " — equipa: " + team + "; de " + row.StartsOn + " até " + today.Format("2006-01-02") + " (fim anterior: " + end + ")."
				found = true
			}
		}
		if !found {
			return db.ErrDatedParticipationTransition
		}
	}
	for _, o := range options {
		if o.ProgrammeID.String() == f.ProgrammeID && o.SeasonID.String() == f.SeasonID {
			if o.Code == "Kayak_Polo" && o.TeamName == "" {
				return db.ErrDatedPoloTeamUnavailable
			}
			category := "sem escalão"
			for _, c := range o.Categories {
				if c.ID.String() == f.CategoryID {
					category = c.Name
				}
			}
			team := "sem equipa"
			if o.Code == "Kayak_Polo" {
				team = o.TeamName + " (equipa partilhada)"
			}
			f.PreviewNew = o.Season + " — " + o.Programme + " — " + category + " — " + team + "; de " + f.StartsOn + " até " + o.SeasonEndsOn + " (fim da época)."
		}
	}
	return nil
}

func (h Classification) versions(ctx context.Context, actor, member uuid.UUID) (string, string, error) {
	return h.Store.Versions(ctx, actor, member)
}
