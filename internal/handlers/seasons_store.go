package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrSeasonConflict = errors.New("season selection changed")
var ErrSeasonDuplicate = errors.New("season code already exists")
var ErrSeasonInvalid = errors.New("invalid season values")

type SeasonInput struct {
	Code, Name       string
	StartsOn, EndsOn time.Time
}
type SeasonSelection struct {
	Target  dbgen.Season
	Current *dbgen.Season
	Version string
}
type SeasonReturnContext struct{ Kind, ID, WeekID, Page, Query string }
type SeasonStore interface {
	Authorize(context.Context, uuid.UUID) error
	HasCurrentSeason(context.Context, uuid.UUID) (bool, error)
	ListSeasons(context.Context, uuid.UUID, int32) ([]dbgen.Season, error)
	GetSelectionState(context.Context, uuid.UUID, uuid.UUID) (SeasonSelection, error)
	CreateSeason(context.Context, uuid.UUID, SeasonInput) (dbgen.Season, error)
	SelectCurrentSeason(context.Context, uuid.UUID, uuid.UUID, string) error
	ResolveReturn(context.Context, uuid.UUID, SeasonReturnContext) (SeasonReturnContext, string, error)
}
type PostgresSeasonStore struct{ Pool *pgxpool.Pool }

func requireSeasonAdmin(ctx context.Context, q *dbgen.Queries, actor uuid.UUID) error {
	allowed, err := q.SeasonAdminAllowed(ctx, actor)
	if err != nil {
		return err
	}
	if !allowed {
		return pgx.ErrNoRows
	}
	return nil
}
func (s PostgresSeasonStore) Authorize(ctx context.Context, actor uuid.UUID) error {
	return requireSeasonAdmin(ctx, dbgen.New(s.Pool), actor)
}
func (s PostgresSeasonStore) ListSeasons(ctx context.Context, actor uuid.UUID, offset int32) ([]dbgen.Season, error) {
	if err := s.Authorize(ctx, actor); err != nil {
		return nil, err
	}
	if offset < 0 || offset > 1980 {
		return nil, ErrSeasonInvalid
	}
	return dbgen.New(s.Pool).ListManagedSeasons(ctx, dbgen.ListManagedSeasonsParams{ActorID: actor, RowOffset: offset})
}
func seasonSelection(ctx context.Context, q *dbgen.Queries, actor, target uuid.UUID) (SeasonSelection, error) {
	rows, err := q.SeasonSelectionRows(ctx, dbgen.SeasonSelectionRowsParams{ActorID: actor, TargetID: target})
	if err != nil {
		return SeasonSelection{}, err
	}
	out := SeasonSelection{}
	for _, r := range rows {
		season := dbgen.Season{ID: r.ID, Code: r.Code, Name: r.Name, StartsOn: r.StartsOn, EndsOn: r.EndsOn, IsCurrent: r.IsCurrent, CreatedAt: r.CreatedAt}
		if r.ID == target {
			out.Target = season
		}
		if r.IsCurrent {
			out.Current = &season
		}
	}
	if out.Target.ID == uuid.Nil {
		return out, pgx.ErrNoRows
	}
	// xmin is only an opaque, short-lived MVCC comparison input. This hash is
	// neither a durable revision nor an audit record, including across restores.
	b, err := json.Marshal(rows)
	if err != nil {
		return out, err
	}
	sum := sha256.Sum256(b)
	out.Version = base64.RawURLEncoding.EncodeToString(sum[:])
	return out, nil
}
func (s PostgresSeasonStore) GetSelectionState(ctx context.Context, actor, target uuid.UUID) (SeasonSelection, error) {
	return seasonSelection(ctx, dbgen.New(s.Pool), actor, target)
}
func (s PostgresSeasonStore) begin(ctx context.Context, actor uuid.UUID) (pgx.Tx, *dbgen.Queries, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (pgx.Tx, *dbgen.Queries, error) { _ = tx.Rollback(ctx); return nil, nil, err }
	if _, err = tx.Exec(ctx, `SET LOCAL lock_timeout='3s'; SET LOCAL statement_timeout='5s'`); err != nil {
		return fail(err)
	}
	// Lock the actual identity and ADMIN assignment against deactivation, erasure,
	// role deletion and role-code changes. Session role flags are never evidence.
	var id uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT u.id FROM users u JOIN user_platform_roles a ON a.user_id=u.id JOIN platform_roles r ON r.id=a.role_id WHERE u.id=$1 AND u.is_active AND u.erased_at IS NULL AND NOT u.is_dependent AND u.date_of_birth<=((now() AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date AND r.code='ADMIN' FOR SHARE OF u,a,r`, actor).Scan(&id); err != nil {
		return fail(err)
	}
	// No-current is a real state, so row locks alone cannot serialize first setup.
	if _, err = tx.Exec(ctx, `LOCK TABLE seasons IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return fail(err)
	}
	q := dbgen.New(tx)
	if err = requireSeasonAdmin(ctx, q, actor); err != nil {
		return fail(err)
	}
	return tx, q, nil
}
func validSeasonInput(in SeasonInput) bool {
	return in.Code == strings.TrimSpace(in.Code) && utf8.RuneCountInString(in.Code) >= 1 && utf8.RuneCountInString(in.Code) <= 20 && in.Name == strings.TrimSpace(in.Name) && utf8.RuneCountInString(in.Name) >= 2 && utf8.RuneCountInString(in.Name) <= 120 && !in.StartsOn.IsZero() && !in.EndsOn.IsZero() && in.StartsOn.Year() >= 1 && in.EndsOn.Year() <= 9999 && !in.StartsOn.After(in.EndsOn)
}
func (s PostgresSeasonStore) CreateSeason(ctx context.Context, actor uuid.UUID, in SeasonInput) (dbgen.Season, error) {
	if !validSeasonInput(in) {
		return dbgen.Season{}, ErrSeasonInvalid
	}
	tx, q, err := s.begin(ctx, actor)
	if err != nil {
		return dbgen.Season{}, err
	}
	defer tx.Rollback(ctx)
	out, err := q.CreateManagedSeason(ctx, dbgen.CreateManagedSeasonParams{ActorID: actor, Code: in.Code, Name: in.Name, StartsOn: pgtype.Date{Time: in.StartsOn, Valid: true}, EndsOn: pgtype.Date{Time: in.EndsOn, Valid: true}})
	var pge *pgconn.PgError
	if errors.As(err, &pge) && pge.Code == "23505" {
		return out, ErrSeasonDuplicate
	}
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
func (s PostgresSeasonStore) SelectCurrentSeason(ctx context.Context, actor, target uuid.UUID, expected string) error {
	tx, q, err := s.begin(ctx, actor)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	current, err := seasonSelection(ctx, q, actor, target)
	if err != nil {
		return err
	}
	if current.Version != expected {
		return ErrSeasonConflict
	}
	if current.Target.IsCurrent {
		return tx.Commit(ctx)
	}
	if err = q.ClearManagedCurrentSeason(ctx, actor); err != nil {
		return err
	}
	n, err := q.SetManagedCurrentSeason(ctx, dbgen.SetManagedCurrentSeasonParams{ActorID: actor, TargetID: target})
	if err != nil {
		return err
	}
	if n != 1 {
		return pgx.ErrNoRows
	}
	return tx.Commit(ctx)
}
func (s PostgresSeasonStore) ResolveReturn(ctx context.Context, actor uuid.UUID, in SeasonReturnContext) (SeasonReturnContext, string, error) {
	if err := s.Authorize(ctx, actor); err != nil {
		return SeasonReturnContext{}, "", err
	}
	fallback := func() (SeasonReturnContext, string, error) {
		return SeasonReturnContext{Kind: "list"}, seasonsPath, nil
	}
	switch in.Kind {
	case "definitions":
		return SeasonReturnContext{Kind: "definitions"}, "/equipa/escaloes", nil
	case "member", "classification":
		id, err := uuid.Parse(in.ID)
		if err != nil || id == uuid.Nil {
			return fallback()
		}
		allowed, err := dbgen.New(s.Pool).SeasonReturnMemberAllowed(ctx, dbgen.SeasonReturnMemberAllowedParams{ActorID: actor, MemberID: id})
		if err != nil {
			return SeasonReturnContext{}, "", err
		}
		if !allowed {
			return fallback()
		}
		in.ID = id.String()
		in.WeekID = ""
		if in.Kind == "classification" {
			return SeasonReturnContext{Kind: in.Kind, ID: in.ID}, "/equipa/classificacao/" + in.ID, nil
		}
		n, valid := trainingGroupPageNumber(in.Page)
		if !valid || utf8.RuneCountInString(in.Query) > 100 {
			return fallback()
		}
		in.Page = ""
		if n > 0 {
			in.Page = strconvSeasonPage(n)
		}
		collection := "/admin/membros"
		v := seasonMemberCollectionQuery(in.Page, in.Query)
		if v != "" {
			collection += "?" + v
		}
		return in, memberDetailPath(id, collection), nil
	case "group":
		id, err := uuid.Parse(in.ID)
		if err != nil || id == uuid.Nil {
			return fallback()
		}
		groups := PostgresTrainingGroupStore{Pool: s.Pool}
		if _, err = groups.GetGroup(ctx, actor, id); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fallback()
			}
			return SeasonReturnContext{}, "", err
		}
		out := SeasonReturnContext{Kind: "group", ID: id.String()}
		dest := trainingGroupsPath + "/" + id.String()
		if week, e := uuid.Parse(in.WeekID); e == nil && week != uuid.Nil {
			allowed, e := groups.AllowsWeek(ctx, actor, id, week)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return SeasonReturnContext{}, "", e
			}
			if e == nil && allowed {
				out.WeekID = week.String()
				dest = structuredPlannerURL(id.String(), week.String(), "")
			}
		}
		return out, dest, nil
	default:
		return fallback()
	}
}

func (s PostgresSeasonStore) HasCurrentSeason(ctx context.Context, actor uuid.UUID) (bool, error) {
	if err := s.Authorize(ctx, actor); err != nil {
		return false, err
	}
	return dbgen.New(s.Pool).ManagedSeasonCurrentExists(ctx, actor)
}
