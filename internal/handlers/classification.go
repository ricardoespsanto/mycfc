package handlers

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	csrf "filippo.io/csrf/gorilla"
	"github.com/cfcoimbra/mycfc/internal/db"
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/featureflags"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrClassificationDenied = errors.New("classification unavailable")

type ClassificationRow struct{ ID, Season, Programme, Team, Category, StartsOn, EndsOn string }
type ClassificationCategory struct {
	ID                uuid.UUID
	Name, Eligibility string
	Eligible          bool
	Masked            bool
}
type ClassificationOption struct {
	SeasonID, ProgrammeID             uuid.UUID
	Season, Programme, Code, TeamName string
	SeasonEndsOn                      string
	Categories                        []ClassificationCategory
}
type ClassificationWrite struct {
	ActorID, MemberID, SeasonID, ProgrammeID uuid.UUID
	CategoryID                               *uuid.UUID
	StartsOn                                 time.Time
	PreviousID                               *uuid.UUID
	AgeExceptionReason                       string
	ExpectedVersion                          string
}
type ClassificationTaxon struct{ Code, Name string }
type ClassificationStore interface {
	Versions(context.Context, uuid.UUID, uuid.UUID) (string, string, error)
	View(context.Context, uuid.UUID, uuid.UUID) (string, []ClassificationRow, error)
	Options(context.Context, uuid.UUID, uuid.UUID) ([]ClassificationOption, error)
	Selections(context.Context, uuid.UUID, uuid.UUID) ([]ClassificationTaxon, []ClassificationTaxon, error)
	Taxonomy(context.Context, uuid.UUID) ([]ClassificationTaxon, []ClassificationTaxon, error)
	ReplaceSelections(context.Context, db.SportAssignmentCorrection) error
	Write(context.Context, ClassificationWrite) error
}
type Classification struct {
	Store       ClassificationStore
	Definitions DefinitionStore
}
type PostgresClassificationStore struct{ Pool *pgxpool.Pool }

// Name-only selection does not grant access to history, selections or option age.
// Team-only grants never authorize this programme-level task.
const classificationSubjectScope = `(` + db.ClassificationSubjectEligibilitySQL + `) AND ` + classificationFirstScope
const classificationFirstScope = `(guardian_authority_is_administrator($1) OR EXISTS (
 SELECT 1 FROM staff_grants g JOIN users actor ON actor.id=g.user_id AND actor.is_active AND actor.erased_at IS NULL
 JOIN seasons s ON s.is_current
 WHERE g.user_id=$1 AND g.capability='COACH' AND g.revoked_at IS NULL AND g.programme_id IS NOT NULL))`
const classificationScope = `(guardian_authority_is_administrator($1) OR EXISTS (
 SELECT 1 FROM staff_grants g JOIN users actor ON actor.id=g.user_id AND actor.is_active AND actor.erased_at IS NULL JOIN user_memberships anchor ON anchor.user_id=$2 AND anchor.programme_id=g.programme_id
 JOIN seasons s ON s.id=anchor.season_id AND s.is_current
 WHERE g.user_id=$1 AND g.capability='COACH' AND g.revoked_at IS NULL
 AND g.programme_id IS NOT NULL AND anchor.starts_on <= (now() AT TIME ZONE 'Europe/Lisbon')::date
 AND (anchor.ends_on IS NULL OR anchor.ends_on >= (now() AT TIME ZONE 'Europe/Lisbon')::date)))`

// Category visibility is tied to the option's own season and programme, not
// merely to any participation within one of the coach's grants.
const classificationOptionScope = `(guardian_authority_is_administrator($1) OR EXISTS (
 SELECT 1 FROM staff_grants g JOIN users actor ON actor.id=g.user_id AND actor.is_active AND actor.erased_at IS NULL
 JOIN user_memberships anchor ON anchor.user_id=$2 AND anchor.season_id=$3 AND anchor.programme_id=$4
 WHERE g.user_id=$1 AND g.capability='COACH' AND g.revoked_at IS NULL AND g.programme_id=$4
 AND anchor.starts_on <= (now() AT TIME ZONE 'Europe/Lisbon')::date
 AND (anchor.ends_on IS NULL OR anchor.ends_on >= (now() AT TIME ZONE 'Europe/Lisbon')::date)))`

func (s PostgresClassificationStore) View(ctx context.Context, actor, member uuid.UUID) (string, []ClassificationRow, error) {
	if s.Pool == nil {
		return "", nil, ErrClassificationDenied
	}
	var name string
	err := s.Pool.QueryRow(ctx, `SELECT member.name FROM users member WHERE member.id=$2 AND `+classificationSubjectScope, actor, member).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrClassificationDenied
	}
	if err != nil {
		return "", nil, err
	}
	rows, err := s.Pool.Query(ctx, `SELECT m.id::text,s.name,p.name_pt,COALESCE(t.name,''),COALESCE(c.name_pt,''),m.starts_on::text,COALESCE(m.ends_on::text,'') FROM user_memberships m JOIN seasons s ON s.id=m.season_id JOIN programmes p ON p.id=m.programme_id LEFT JOIN teams t ON t.id=m.team_id LEFT JOIN competition_categories c ON c.id=m.competition_category_id WHERE m.user_id=$2 AND EXISTS(SELECT 1 FROM users member WHERE member.id=m.user_id AND `+db.ClassificationSubjectEligibilitySQL+`) AND `+classificationScope+` AND (guardian_authority_is_administrator($1) OR EXISTS(SELECT 1 FROM staff_grants g WHERE g.user_id=$1 AND g.capability='COACH' AND g.revoked_at IS NULL AND g.programme_id=m.programme_id AND g.programme_id IS NOT NULL AND s.is_current)) ORDER BY m.starts_on DESC`, actor, member)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	var result []ClassificationRow
	for rows.Next() {
		var x ClassificationRow
		if err := rows.Scan(&x.ID, &x.Season, &x.Programme, &x.Team, &x.Category, &x.StartsOn, &x.EndsOn); err != nil {
			return "", nil, err
		}
		result = append(result, x)
	}
	return name, result, rows.Err()
}
func (s PostgresClassificationStore) Options(ctx context.Context, actor, member uuid.UUID) ([]ClassificationOption, error) {
	if _, _, err := s.View(ctx, actor, member); err != nil {
		return nil, err
	}
	if s.Pool == nil {
		return nil, ErrClassificationDenied
	}
	rows, err := s.Pool.Query(ctx, `SELECT s.id,p.id,s.name,p.name_pt,p.code,COALESCE((SELECT t.name FROM teams t WHERE t.season_id=s.id AND t.programme_id=p.id LIMIT 1),''),s.ends_on::text FROM seasons s CROSS JOIN programmes p WHERE s.is_current AND (guardian_authority_is_administrator($1) OR EXISTS(SELECT 1 FROM staff_grants g JOIN users actor ON actor.id=g.user_id AND actor.is_active AND actor.erased_at IS NULL WHERE g.user_id=$1 AND g.capability='COACH' AND g.revoked_at IS NULL AND g.programme_id=p.id)) ORDER BY p.name_pt`, actor)
	if err != nil {
		return nil, err
	}
	var out []ClassificationOption
	for rows.Next() {
		var x ClassificationOption
		if err := rows.Scan(&x.SeasonID, &x.ProgrammeID, &x.Season, &x.Programme, &x.Code, &x.TeamName, &x.SeasonEndsOn); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for i := range out {
		categories, e := s.Pool.Query(ctx, `SELECT c.id,c.name_pt,CASE WHEN `+classificationOptionScope+` THEN COALESCE(c.birth_date_from::text,'sem limite')||' — '||COALESCE(c.birth_date_to::text,'sem limite')||'; idade no início da época: '||CASE WHEN u.date_of_birth IS NULL THEN 'não disponível' ELSE EXTRACT(YEAR FROM age(season.starts_on,u.date_of_birth))::int::text||' anos' END ELSE 'Elegibilidade verificada ao guardar' END,COALESCE(`+classificationOptionScope+` AND (c.birth_date_from IS NULL OR u.date_of_birth>=c.birth_date_from) AND (c.birth_date_to IS NULL OR u.date_of_birth<=c.birth_date_to),false) FROM competition_categories c JOIN seasons season ON season.id=c.season_id JOIN users u ON u.id=$2 WHERE EXISTS(SELECT 1 FROM users member WHERE member.id=u.id AND `+db.ClassificationSubjectEligibilitySQL+`) AND c.season_id=$3 AND c.programme_id=$4 AND (guardian_authority_is_administrator($1) OR EXISTS(SELECT 1 FROM staff_grants g JOIN users actor ON actor.id=g.user_id AND actor.is_active AND actor.erased_at IS NULL WHERE g.user_id=$1 AND g.capability='COACH' AND g.revoked_at IS NULL AND g.programme_id=$4 AND season.is_current)) ORDER BY c.name_pt`, actor, member, out[i].SeasonID, out[i].ProgrammeID)
		if e != nil {
			return nil, e
		}
		for categories.Next() {
			var c ClassificationCategory
			if e = categories.Scan(&c.ID, &c.Name, &c.Eligibility, &c.Eligible); e != nil {
				categories.Close()
				return nil, e
			}
			c.Masked = c.Eligibility == "Elegibilidade verificada ao guardar"
			out[i].Categories = append(out[i].Categories, c)
		}
		e = categories.Err()
		categories.Close()
		if e != nil {
			return nil, e
		}
	}
	return out, nil
}
func (s PostgresClassificationStore) EligibilityOnWrite(ctx context.Context, actor, member, season, programme, category uuid.UUID) (bool, error) {
	if _, _, err := s.View(ctx, actor, member); err != nil {
		return false, err
	}
	var eligible bool
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE((c.birth_date_from IS NULL OR member.date_of_birth >= c.birth_date_from) AND (c.birth_date_to IS NULL OR member.date_of_birth <= c.birth_date_to),false)
 FROM competition_categories c JOIN seasons s ON s.id=c.season_id AND s.is_current
 JOIN users member ON member.id=$2
 WHERE `+classificationSubjectScope+` AND c.id=$5 AND c.season_id=$3 AND c.programme_id=$4
 AND (guardian_authority_is_administrator($1) OR EXISTS (
 SELECT 1 FROM staff_grants g JOIN users actor ON actor.id=g.user_id AND actor.is_active AND actor.erased_at IS NULL
 WHERE g.user_id=$1 AND g.capability='COACH' AND g.revoked_at IS NULL AND g.programme_id=$4))`, actor, member, season, programme, category).Scan(&eligible)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrClassificationDenied
	}
	return eligible, err
}

func (s PostgresClassificationStore) Selections(ctx context.Context, actor, member uuid.UUID) ([]ClassificationTaxon, []ClassificationTaxon, error) {
	if _, _, err := s.View(ctx, actor, member); err != nil {
		return nil, nil, err
	}
	if s.Pool == nil {
		return nil, nil, ErrClassificationDenied
	}
	rows, err := s.Pool.Query(ctx, `SELECT sm.code,sm.name_pt FROM person_sporting_modalities a JOIN sporting_modalities sm ON sm.code=a.modality_code WHERE a.user_id=$2 AND EXISTS(SELECT 1 FROM users member WHERE member.id=a.user_id AND `+db.ClassificationSubjectEligibilitySQL+`) AND `+classificationScope+` ORDER BY sm.code`, actor, member)
	if err != nil {
		return nil, nil, err
	}
	var mods []ClassificationTaxon
	for rows.Next() {
		var x ClassificationTaxon
		if err = rows.Scan(&x.Code, &x.Name); err != nil {
			rows.Close()
			return nil, nil, err
		}
		mods = append(mods, x)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	rows, err = s.Pool.Query(ctx, `SELECT c.code,c.name_pt FROM person_canoe_craft_classes a JOIN canoe_craft_classes c ON c.code=a.craft_code WHERE a.user_id=$2 AND EXISTS(SELECT 1 FROM users member WHERE member.id=a.user_id AND `+db.ClassificationSubjectEligibilitySQL+`) AND `+classificationScope+` ORDER BY c.code`, actor, member)
	if err != nil {
		return nil, nil, err
	}
	var crafts []ClassificationTaxon
	for rows.Next() {
		var x ClassificationTaxon
		if err = rows.Scan(&x.Code, &x.Name); err != nil {
			rows.Close()
			return nil, nil, err
		}
		crafts = append(crafts, x)
	}
	err = rows.Err()
	rows.Close()
	return mods, crafts, err
}
func (s PostgresClassificationStore) Taxonomy(ctx context.Context, member uuid.UUID) ([]ClassificationTaxon, []ClassificationTaxon, error) {
	if s.Pool == nil {
		return nil, nil, ErrClassificationDenied
	}
	q := dbgen.New(s.Pool)
	mods, err := q.ListSportingModalities(ctx)
	if err != nil {
		return nil, nil, err
	}
	crafts, err := q.ListCanoeCraftClasses(ctx)
	if err != nil {
		return nil, nil, err
	}
	var m, c []ClassificationTaxon
	for _, v := range mods {
		m = append(m, ClassificationTaxon{v.Code, v.NamePt})
	}
	for _, v := range crafts {
		c = append(c, ClassificationTaxon{v.Code, v.NamePt})
	}
	return m, c, nil
}

// ReplaceSelections submits one explicitly identified complete desired set. Omitted
// checkboxes mean removal, never an additive operation.
func (h Classification) ReplaceSelections(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	actor, ok := currentUser(r.Context())
	member, err := uuid.Parse(r.PathValue("id"))
	if !ok || err != nil || actor.ID == uuid.Nil || h.Store == nil {
		http.NotFound(w, r)
		return
	}
	if _, _, err = h.Store.View(r.Context(), actor.ID, member); err != nil {
		h.failure(w, r, err)
		return
	}
	if checker, ok := h.Store.(interface {
		CanCorrect(context.Context, uuid.UUID, uuid.UUID) (bool, error)
	}); ok {
		allowed, e := checker.CanCorrect(r.Context(), actor.ID, member)
		if e != nil {
			h.failure(w, r, e)
			return
		}
		if !allowed {
			http.NotFound(w, r)
			return
		}
	}
	if err = r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido", http.StatusBadRequest)
		return
	}
	kind := db.SportAssignmentKind(r.PostForm.Get("kind"))
	f := classificationForm{CorrectionKind: string(kind), CorrectionCodes: append([]string(nil), r.PostForm["codes"]...), CorrectionReason: r.PostForm.Get("reason"), CorrectionSubmitted: true, OriginalToken: r.PostForm.Get("original_token")}
	if kind != db.SportingModalityAssignment && kind != db.CanoeCraftAssignment {
		f.CorrectionMessage = "Escolha o tipo de seleção disponível."
		h.render(w, r, 422, f)
		return
	}
	mods, crafts, err := h.Store.Taxonomy(r.Context(), member)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	list := mods
	if kind == db.CanoeCraftAssignment {
		list = crafts
	}
	validCodes := make(map[string]bool, len(list))
	for _, item := range list {
		validCodes[item.Code] = true
	}
	seen := map[string]bool{}
	valid := len(f.CorrectionCodes) <= len(list) && len(strings.TrimSpace(f.CorrectionReason)) > 0 && len(strings.TrimSpace(f.CorrectionReason)) <= 500
	for _, code := range f.CorrectionCodes {
		if !validCodes[code] || seen[code] {
			valid = false
		}
		seen[code] = true
	}
	version, validToken := verifyClassificationConfirmation(r.PostForm.Get("original_token"), confirmationBinding(actor.ID, member, string(kind), nil), time.Now())
	if valid {
		currentVersion, _, e := h.versions(r.Context(), actor.ID, member)
		if e != nil {
			h.failure(w, r, e)
			return
		}
		if !validToken || version != currentVersion {
			f.CorrectionMessage = "A seleção original expirou, não é válida ou as seleções mudaram. Recarregue a página e reveja as seleções atuais; nenhuma alteração foi guardada."
			h.render(w, r, 409, f)
			return
		}
	}
	if kind == db.CanoeCraftAssignment && len(f.CorrectionCodes) > 0 {
		currentMods, _, e := h.Store.Selections(r.Context(), actor.ID, member)
		if e != nil {
			h.failure(w, r, e)
			return
		}
		parent := false
		for _, item := range currentMods {
			if item.Code == "CANOEING" {
				parent = true
			}
		}
		if !parent {
			valid = false
		}
	}
	if !valid {
		f.CorrectionMessage = "Reveja as seleções e indique um motivo (até 500 caracteres). Para embarcações, é necessária Canoagem."
		h.render(w, r, 422, f)
		return
	}
	err = h.Store.ReplaceSelections(r.Context(), db.SportAssignmentCorrection{ActorID: actor.ID, MemberID: member, Kind: kind, Codes: f.CorrectionCodes, Reason: f.CorrectionReason, ExpectedVersion: version})
	if errors.Is(err, db.ErrSportAssignmentForbidden) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		f.CorrectionMessage = "As seleções podem ter mudado. Recarregue a página e reveja as seleções atuais antes de tentar novamente; nenhuma alteração foi guardada."
		if errors.Is(err, db.ErrSportAssignmentInvalid) {
			h.render(w, r, 422, f)
		} else {
			h.render(w, r, 409, f)
		}
		return
	}
	h.render(w, r, 200, classificationForm{Success: "Seleções desportivas atualizadas com motivo registado."})
}

func (s PostgresClassificationStore) ReplaceSelections(ctx context.Context, in db.SportAssignmentCorrection) error {
	return (db.SportAssignmentService{Pool: s.Pool}).ReplaceForClassification(ctx, in)
}

func (s PostgresClassificationStore) Write(ctx context.Context, in ClassificationWrite) error {
	svc := db.DatedParticipationService{Pool: s.Pool}
	if in.AgeExceptionReason != "" {
		if in.CategoryID == nil {
			return db.ErrAgeExceptionReason
		}
		value := db.AgeExceptionAssignment{ActorID: in.ActorID, MemberID: in.MemberID, SeasonID: in.SeasonID, ProgrammeID: in.ProgrammeID, CategoryID: *in.CategoryID, StartsOn: in.StartsOn, Reason: in.AgeExceptionReason, ExpectedVersion: in.ExpectedVersion}
		var err error
		if in.PreviousID != nil {
			_, err = svc.TransitionNextDayAgeException(ctx, *in.PreviousID, value)
		} else {
			_, err = svc.CreateAgeException(ctx, value)
		}
		return err
	}
	value := db.OrdinaryDatedAssignment{ActorID: in.ActorID, MemberID: in.MemberID, SeasonID: in.SeasonID, ProgrammeID: in.ProgrammeID, CategoryID: in.CategoryID, StartsOn: in.StartsOn, ExpectedVersion: in.ExpectedVersion}
	var err error
	if in.PreviousID != nil {
		_, err = svc.TransitionNextDay(ctx, *in.PreviousID, value)
	} else {
		_, err = svc.CreateOrdinaryAssignment(ctx, value)
	}
	return err
}

func classificationState(rows []ClassificationRow, now time.Time) string {
	today := now.Format("2006-01-02")
	scheduled, ended := false, false
	for _, x := range rows {
		if x.StartsOn <= today && (x.EndsOn == "" || x.EndsOn >= today) {
			return "Em vigor"
		}
		if x.StartsOn > today {
			scheduled = true
		} else {
			ended = true
		}
	}
	if scheduled {
		return "Agendada"
	}
	if ended {
		return "Terminada"
	}
	return "Sem participação ativa"
}
func (s PostgresClassificationStore) CanCorrect(ctx context.Context, actor, member uuid.UUID) (bool, error) {
	if s.Pool == nil {
		return false, ErrClassificationDenied
	}
	var allowed bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users member WHERE member.id=$2 AND `+classificationSubjectScope+` AND `+classificationScope+`)`, actor, member).Scan(&allowed)
	return allowed, err
}

func (h Classification) Get(w http.ResponseWriter, r *http.Request) {
	h.render(w, r, http.StatusOK, classificationForm{})
}

type classificationForm struct {
	SeasonID, ProgrammeID, CategoryID, StartsOn, PreviousID, Message, Success string
	AgeExceptionReason, ErrorTarget                                           string
	Invalid                                                                   bool
	CorrectionKind, CorrectionReason, CorrectionMessage                       string
	CorrectionCodes                                                           []string
	CorrectionSubmitted                                                       bool
	PreviewToken, OriginalToken, PreviewOld, PreviewNew                       string
}

func (h Classification) Post(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	actor, ok := currentUser(r.Context())
	member, e := uuid.Parse(r.PathValue("id"))
	if !ok || e != nil || actor.ID == uuid.Nil || h.Store == nil {
		http.NotFound(w, r)
		return
	}
	_, _, err := h.Store.View(r.Context(), actor.ID, member)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	f := classificationForm{}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Pedido inválido", http.StatusBadRequest)
		return
	}
	f.SeasonID = r.PostForm.Get("season_id")
	f.ProgrammeID = r.PostForm.Get("programme_id")
	if scope := r.PostForm.Get("scope"); scope != "" {
		parts := strings.Split(scope, ":")
		if len(parts) == 2 {
			f.SeasonID = parts[0]
			f.ProgrammeID = parts[1]
		}
	}
	f.CategoryID = r.PostForm.Get("category_id")
	f.StartsOn = r.PostForm.Get("starts_on")
	f.PreviousID = r.PostForm.Get("previous_id")
	f.AgeExceptionReason = r.PostForm.Get("age_exception_reason")
	_, version, versionErr := h.versions(r.Context(), actor.ID, member)
	if versionErr != nil {
		h.failure(w, r, versionErr)
		return
	}
	options, err := h.Store.Options(r.Context(), actor.ID, member)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	season, e1 := uuid.Parse(f.SeasonID)
	programme, e2 := uuid.Parse(f.ProgrammeID)
	start, e3 := time.Parse("2006-01-02", f.StartsOn)
	allowed := false
	code := ""
	categoryAllowed := f.CategoryID == ""
	categoryMismatch := false
	for _, o := range options {
		if o.SeasonID == season && o.ProgrammeID == programme {
			allowed = true
			code = o.Code
			for _, c := range o.Categories {
				if c.ID.String() == f.CategoryID {
					categoryAllowed = true
					categoryMismatch = !c.Eligible
				}
			}
		}
	}
	if code == "Competition" && f.CategoryID == "" {
		categoryAllowed = false
	}
	if (code == "Leisure" || code == "Kayak_Polo") && f.CategoryID != "" {
		categoryAllowed = false
	}
	loc, _ := time.LoadLocation("Europe/Lisbon")
	today := time.Now().In(loc)
	tomorrow := today.AddDate(0, 0, 1).Format("2006-01-02")
	if r.PostForm.Has("actor_id") || r.PostForm.Has("age_exception_by_id") || r.PostForm.Has("age_exception_at") || r.PostForm.Has("member_id") || e1 != nil || e2 != nil || e3 != nil || !allowed || !categoryAllowed || f.StartsOn != today.Format("2006-01-02") && f.StartsOn != tomorrow || r.PostForm.Has("ends_on") || r.PostForm.Has("modality_id") || r.PostForm.Has("team_id") || r.PostForm.Has("craft_class_id") {
		f.Message = "Verifique a época, participação e data. Apenas hoje ou amanhã; sem correções retroativas nesta tarefa."
		f.ErrorTarget = "starts_on"
		f.Invalid = true
		h.render(w, r, http.StatusUnprocessableEntity, f)
		return
	}
	// Options intentionally mask unanchored scopes. Recompute privately for the
	// write path; the transactional service still enforces the final eligibility.
	if f.CategoryID != "" {
		if id, parseErr := uuid.Parse(f.CategoryID); parseErr == nil {
			if checker, ok := h.Store.(interface {
				EligibilityOnWrite(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (bool, error)
			}); ok {
				eligible, checkErr := checker.EligibilityOnWrite(r.Context(), actor.ID, member, season, programme, id)
				if checkErr != nil {
					h.failure(w, r, checkErr)
					return
				}
				categoryMismatch = !eligible
			}
		}
	}
	reason := strings.TrimSpace(f.AgeExceptionReason)
	if (categoryMismatch && (utf8.RuneCountInString(reason) < 2 || utf8.RuneCountInString(reason) > 500)) || (!categoryMismatch && r.PostForm.Has("age_exception_reason") && reason != "") {
		f.Message = "Indique um motivo entre 2 e 500 caracteres apenas para um escalão fora da elegibilidade; não inclua dados sensíveis."
		f.ErrorTarget = "age_exception_reason"
		f.Invalid = true
		h.render(w, r, 422, f)
		return
	}
	var category, previous *uuid.UUID
	if f.CategoryID != "" {
		id, e := uuid.Parse(f.CategoryID)
		if e != nil {
			f.Message = "Escalão inválido."
			f.Invalid = true
			h.render(w, r, 422, f)
			return
		}
		category = &id
	}
	if f.PreviousID != "" {
		id, e := uuid.Parse(f.PreviousID)
		if e != nil || f.StartsOn != tomorrow {
			f.Message = "A transição exige a data de amanhã e uma participação anterior válida."
			f.Invalid = true
			h.render(w, r, 422, f)
			return
		}
		previous = &id
	}
	// The service rechecks actor scope, current season, interval and age mismatch
	// inside its transaction. Never accept actor provenance from form data.
	if !categoryMismatch {
		reason = ""
	}
	in := ClassificationWrite{ActorID: actor.ID, MemberID: member, SeasonID: season, ProgrammeID: programme, CategoryID: category, StartsOn: start, PreviousID: previous, AgeExceptionReason: reason}
	binding := confirmationBinding(actor.ID, member, "DATED", in)
	if r.PostForm.Get("confirm") != "yes" {
		if err := h.datedPreview(r.Context(), actor.ID, member, options, &f, today); err != nil {
			f.Message = "Reveja a participação anterior, as datas e a equipa antes de pré-visualizar."
			f.Invalid = true
			h.render(w, r, 409, f)
			return
		}
		_, after, err := h.versions(r.Context(), actor.ID, member)
		if err != nil {
			h.failure(w, r, err)
			return
		}
		if after != version {
			f.Invalid = true
			f.Message = "A participação mudou. Recarregue e reveja antes de confirmar."
			h.render(w, r, 409, f)
			return
		}
		f.PreviewToken = signClassificationConfirmation(binding, version, time.Now())
		h.render(w, r, 200, f)
		return
	}
	expected, validToken := verifyClassificationConfirmation(r.PostForm.Get("preview_token"), binding, time.Now())
	if !validToken || expected != version {
		f.Invalid = true
		f.Message = "A pré-visualização expirou ou a participação mudou. Reveja os valores e volte a pré-visualizar; nada foi guardado."
		f.ErrorTarget = "starts_on"
		h.render(w, r, 409, f)
		return
	}
	in.ExpectedVersion = expected
	err = h.Store.Write(r.Context(), in)
	if err != nil {
		if errors.Is(err, db.ErrDatedParticipationForbidden) {
			http.NotFound(w, r)
			return
		}
		if errors.Is(err, db.ErrDatedPoloTeamUnavailable) {
			f.Message = "Ainda não existe uma equipa partilhada de Kayak Polo nesta época. Peça a criação da equipa antes de guardar; nenhuma foi criada automaticamente."
			f.ErrorTarget = "scope"
			f.Invalid = true
			h.render(w, r, http.StatusConflict, f)
			return
		}
		f.Message = "Não foi possível guardar. Confirme a participação atual e as datas antes de tentar novamente."
		f.ErrorTarget = "starts_on"
		f.Invalid = true
		h.render(w, r, http.StatusConflict, f)
		return
	}
	h.render(w, r, http.StatusOK, classificationForm{Success: "Guardada: " + code + " a partir de " + f.StartsOn + "."})
}
func (h Classification) failure(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "no-store")
	if errors.Is(err, ErrClassificationDenied) {
		http.NotFound(w, r)
	} else {
		http.Error(w, "Serviço indisponível", http.StatusServiceUnavailable)
	}
}
func (h Classification) render(w http.ResponseWriter, r *http.Request, status int, f classificationForm) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	actor, ok := currentUser(r.Context())
	id, e := uuid.Parse(r.PathValue("id"))
	if !ok || e != nil || actor.ID == uuid.Nil || h.Store == nil {
		http.NotFound(w, r)
		return
	}
	sportVersion, _, err := h.versions(r.Context(), actor.ID, id)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	name, rows, err := h.Store.View(r.Context(), actor.ID, id)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	options, err := h.Store.Options(r.Context(), actor.ID, id)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	mods, crafts, err := h.Store.Taxonomy(r.Context(), id)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	selectedMods, selectedCrafts, err := h.Store.Selections(r.Context(), actor.ID, id)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	afterSport, _, err := h.versions(r.Context(), actor.ID, id)
	if err != nil {
		h.failure(w, r, err)
		return
	}
	if sportVersion != afterSport {
		http.Error(w, "A classificação mudou. Recarregue a página para rever.", http.StatusConflict)
		return
	}
	sportToken := signClassificationConfirmation(confirmationBinding(actor.ID, id, "SPORT", nil), sportVersion, time.Now())
	craftToken := signClassificationConfirmation(confirmationBinding(actor.ID, id, "CRAFT", nil), sportVersion, time.Now())
	if f.CorrectionSubmitted {
		if f.CorrectionKind == "SPORT" {
			sportToken = f.OriginalToken
		} else {
			craftToken = f.OriginalToken
		}
	}
	canCorrect := true
	if checker, ok := h.Store.(interface {
		CanCorrect(context.Context, uuid.UUID, uuid.UUID) (bool, error)
	}); ok {
		canCorrect, err = checker.CanCorrect(r.Context(), actor.ID, id)
		if err != nil {
			h.failure(w, r, err)
			return
		}
	}
	canDefine := false
	if h.Definitions != nil {
		scopes, scopeErr := h.Definitions.Scopes(r.Context(), actor.ID)
		canDefine = scopeErr == nil && len(scopes) > 0
	}
	data := struct {
		Name, State, Success, CSRF, MemberID             string
		Rows                                             []ClassificationRow
		Options                                          []ClassificationOption
		Modalities, Crafts, SelectedMods, SelectedCrafts []ClassificationTaxon
		Form                                             classificationForm
		CanDefine                                        bool
		ChangePersonURL                                  string
		CanCorrect                                       bool
		CanManageCrews                                   bool
		SportToken, CraftToken                           string
	}{name, classificationState(rows, time.Now().In(lisbonLocation())), "", string(csrf.Token(r)), id.String(), rows, options, mods, crafts, selectedMods, selectedCrafts, f, canDefine, classificationSearchURL(r.URL.Query().Get("q"), 1), canCorrect, (actor.IsAdmin || actor.CanManageEvents) && featureflags.Available(actor.FeatureModes, featureflags.StructuredTrainingPlanning, actor.IsAdmin), sportToken, craftToken}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	classificationTemplate.Execute(w, data)
}
func lisbonLocation() *time.Location { l, _ := time.LoadLocation("Europe/Lisbon"); return l }

func selectedTaxon(codes []string, code string) bool {
	for _, item := range codes {
		if item == code {
			return true
		}
	}
	return false
}
func currentTaxon(codes []ClassificationTaxon, code string) bool {
	for _, item := range codes {
		if item.Code == code {
			return true
		}
	}
	return false
}

var classificationTemplate = template.Must(template.New("classification").Funcs(template.FuncMap{"selectedTaxon": selectedTaxon, "currentTaxon": currentTaxon}).Parse(`<!doctype html><html lang="pt-PT"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Classificação desportiva | MyCFCoimbra</title><link rel="stylesheet" href="/equipa/classificacao.css"><main><h1 id="task-heading" tabindex="-1" {{if not (or .Form.Invalid .Form.CorrectionMessage .Form.PreviewToken)}}autofocus{{end}}>Alterar participação: {{.Name}}</h1><p><a href="{{.ChangePersonURL}}">Alterar pessoa</a></p><p>Estado: {{.State}}</p>{{if .CanManageCrews}}<p><a href="/admin/treinos/estruturados#training-variations">Gerir tripulações</a> — continue no planeamento de treinos; esta ligação não inscreve a pessoa automaticamente.</p>{{end}}{{if .CanDefine}}<p><a href="/equipa/escaloes">Gerir escalões da época</a> — crie uma definição antes de atribuir um escalão de Competição ou Iniciação.</p>{{end}}{{if eq .Form.Success ""}}{{else}}<p role="status">{{.Name}} — {{.Form.Success}}</p>{{end}}{{if .Form.Invalid}}<div class="error" id="error-summary" role="alert" tabindex="-1" autofocus><strong>Não foi possível guardar</strong><p><a href="#{{if .Form.ErrorTarget}}{{.Form.ErrorTarget}}{{else}}starts_on{{end}}">{{.Form.Message}}</a></p></div>{{end}}<section aria-label="Histórico de participação"><h2>Participações e histórico</h2>{{if .Rows}}<ol>{{range .Rows}}<li>{{.Season}} — {{.Programme}}{{if .Team}} — equipa {{.Team}}{{end}}{{if .Category}} ({{.Category}}){{end}}: de <time>{{.StartsOn}}</time> {{if .EndsOn}}até <time>{{.EndsOn}}</time>{{else}}sem data de fim{{end}}. <a href="#starts_on">Rever transição</a></li>{{end}}</ol>{{else}}<p>Sem participação ativa</p>{{end}}</section><form method="post"><input type="hidden" name="csrf_token" value="{{.CSRF}}">{{if .Form.PreviewToken}}<section aria-labelledby="preview-heading"><h2 id="preview-heading" tabindex="-1" autofocus>Rever antes de confirmar: {{.Name}}</h2><h3>Intervalo anterior</h3><p>{{.Form.PreviewOld}}</p><h3>Intervalo novo</h3><p>{{.Form.PreviewNew}}</p><p>Confirme apenas depois de rever as datas, a participação, o escalão e a equipa. Alterar os valores exige nova pré-visualização. O histórico de eventos e treinos não é alterado. Esta pré-visualização expira em 15 minutos.</p></section><input type="hidden" name="preview_token" value="{{.Form.PreviewToken}}">{{end}}<fieldset><legend>Participação datada</legend><label for="scope">Época e participação</label><select id="scope" name="scope" aria-describedby="polo-team-help{{if eq .Form.ErrorTarget "scope"}} error-summary{{end}}" {{if eq .Form.ErrorTarget "scope"}}aria-invalid="true"{{end}} required><option value="">Escolha uma opção</option>{{range .Options}}<option value="{{.SeasonID}}:{{.ProgrammeID}}" {{if eq $.Form.ProgrammeID .ProgrammeID.String}}selected{{end}}>{{.Season}} — {{.Programme}}{{if eq .Code "Kayak_Polo"}}{{if .TeamName}} — equipa: {{.TeamName}}{{else}} — equipa ainda não criada{{end}}{{end}}</option>{{end}}</select><p id="polo-team-help">Para Kayak Polo, a equipa partilhada da época é atribuída automaticamente; se não existir, não é possível guardar.</p><label for="starts_on">Data de início</label><input type="date" id="starts_on" name="starts_on" value="{{.Form.StartsOn}}" aria-describedby="date-help{{if .Form.Invalid}} error-summary{{end}}" {{if .Form.Invalid}}aria-invalid="true"{{end}} required><p id="date-help">Atribuição: hoje ou amanhã. Transição: amanhã; a participação anterior termina hoje. Não altera o histórico anterior.</p><label for="previous_id">Participação anterior (apenas para transição amanhã)</label><select id="previous_id" name="previous_id"><option value="">Nova atribuição</option>{{range .Rows}}<option value="{{.ID}}" {{if eq $.Form.PreviousID .ID}}selected{{end}}>{{.Programme}} — {{.StartsOn}} {{.EndsOn}}</option>{{end}}</select><label for="category_id">Escalão (obrigatório para Competição, opcional para Iniciação)</label><select id="category_id" name="category_id" aria-describedby="eligibility-help"><option value="">Sem escalão</option>{{range .Options}}{{if .Categories}}<optgroup label="{{.Season}} — {{.Programme}}">{{range .Categories}}<option value="{{.ID}}" {{if not .Eligible}}data-mismatch="true"{{end}} {{if eq $.Form.CategoryID .ID.String}}selected{{end}}>{{.Name}}{{if .Masked}} (elegibilidade verificada ao guardar){{else}}{{if not .Eligible}} (fora da elegibilidade; exige motivo){{else}} (elegível){{end}}{{end}}</option>{{end}}</optgroup>{{end}}{{end}}</select><p id="eligibility-help">A elegibilidade considera a data de nascimento face ao intervalo configurado para o escalão na época. Se escolher um escalão fora do intervalo, indique o motivo.</p>{{range .Options}}{{if .Categories}}<p>{{.Season}} — {{.Programme}}:</p><ul>{{range .Categories}}<li>{{.Name}} — nascimento {{.Eligibility}}; {{if .Masked}}elegibilidade verificada ao guardar{{else}}{{if .Eligible}}elegível{{else}}fora da elegibilidade; exige motivo{{end}}{{end}}.</li>{{end}}</ul>{{end}}{{end}}<div id="age-reason-group"><label for="age_exception_reason">Registar exceção de idade — motivo (apenas para escalão fora da elegibilidade)</label><textarea id="age_exception_reason" name="age_exception_reason" maxlength="500" aria-describedby="age-reason-help{{if eq .Form.ErrorTarget "age_exception_reason"}} error-summary{{end}}" {{if eq .Form.ErrorTarget "age_exception_reason"}}aria-invalid="true"{{end}}>{{.Form.AgeExceptionReason}}</textarea><p id="age-reason-help">Obrigatório apenas para escalão fora da elegibilidade (2 a 500 caracteres). Descreva apenas a diferença de idade; não inclua dados de saúde nem de terceiros. O motivo é reservado à equipa e não aparece no histórico.</p></div><p>Se a equipa de Kayak Polo ainda não existir, peça a sua criação; este formulário não cria equipas. Correções retroativas e outras equipas requerem outra operação autorizada.</p></fieldset><p>Guardar participação apresenta primeiro uma pré-visualização; só a confirmação seguinte grava a alteração.</p><button type="submit">Guardar participação</button>{{if .Form.PreviewToken}}<button type="submit" name="confirm" value="yes">Confirmar participação</button>{{end}}</form>{{if .CanCorrect}}<section aria-labelledby="taxon-title"><h2 id="taxon-title">Corrigir modalidades e embarcações</h2><h3>Seleções atuais</h3>{{if or .SelectedMods .SelectedCrafts}}<ul>{{range .SelectedMods}}<li>Modalidade: {{.Name}}</li>{{end}}{{range .SelectedCrafts}}<li>Embarcação: {{.Code}} — {{.Name}}</li>{{end}}</ul>{{else}}<p>Sem modalidades ou embarcações registadas.</p>{{end}}<p>Cada formulário envia o conjunto completo pretendido para o seu tipo. Desmarcar todas remove todas as seleções desse tipo. Remover Canoagem remove também todas as suas embarcações; registe o motivo. O histórico de participação não é alterado. No motivo, descreva apenas a correção; não inclua dados de saúde nem informações sensíveis de menores.</p>{{if .Form.CorrectionMessage}}<div class="error" id="error-summary" role="alert" tabindex="-1" autofocus><strong>Não foi possível guardar a correção</strong><p><a href="#{{if eq .Form.CorrectionKind "CRAFT"}}craft-reason{{else}}sport-reason{{end}}">{{.Form.CorrectionMessage}}</a></p><p><a href="/equipa/classificacao/{{.MemberID}}">Recarregar e rever seleções atuais</a> (descarta a proposta ainda não guardada).</p></div>{{end}}<form method="post" action="/equipa/classificacao/{{.MemberID}}/selecoes"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="kind" value="SPORT"><input type="hidden" name="original_token" value="{{.SportToken}}"><fieldset><legend>Modalidades pretendidas</legend>{{range .Modalities}}<label><input type="checkbox" name="codes" value="{{.Code}}" {{if and $.Form.CorrectionSubmitted (eq $.Form.CorrectionKind "SPORT")}}{{if selectedTaxon $.Form.CorrectionCodes .Code}}checked{{end}}{{else}}{{if currentTaxon $.SelectedMods .Code}}checked{{end}}{{end}}> {{.Name}}</label>{{end}}</fieldset><label for="sport-reason">Motivo da correção de modalidades (obrigatório)</label><textarea id="sport-reason" name="reason" maxlength="500" required {{if and .Form.CorrectionMessage (eq .Form.CorrectionKind "SPORT")}}aria-invalid="true" aria-describedby="error-summary"{{end}}>{{if eq .Form.CorrectionKind "SPORT"}}{{.Form.CorrectionReason}}{{end}}</textarea><button type="submit">Guardar conjunto de modalidades</button></form><form method="post" action="/equipa/classificacao/{{.MemberID}}/selecoes"><input type="hidden" name="csrf_token" value="{{.CSRF}}"><input type="hidden" name="kind" value="CRAFT"><input type="hidden" name="original_token" value="{{.CraftToken}}"><fieldset><legend>Embarcações de Canoagem pretendidas</legend>{{range .Crafts}}<label><input type="checkbox" name="codes" value="{{.Code}}" {{if and $.Form.CorrectionSubmitted (eq $.Form.CorrectionKind "CRAFT")}}{{if selectedTaxon $.Form.CorrectionCodes .Code}}checked{{end}}{{else}}{{if currentTaxon $.SelectedCrafts .Code}}checked{{end}}{{end}}> {{.Code}} — {{.Name}}</label>{{end}}</fieldset><p>Para adicionar embarcações, guarde primeiro Canoagem nas modalidades.</p><label for="craft-reason">Motivo da correção de embarcações (obrigatório)</label><textarea id="craft-reason" name="reason" maxlength="500" required {{if and .Form.CorrectionMessage (eq .Form.CorrectionKind "CRAFT")}}aria-invalid="true" aria-describedby="error-summary"{{end}}>{{if eq .Form.CorrectionKind "CRAFT"}}{{.Form.CorrectionReason}}{{end}}</textarea><button type="submit">Guardar conjunto de embarcações</button></form></section>{{end}}</main></html>`))
