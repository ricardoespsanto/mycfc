package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type TrainingSetupContext struct{ GroupID, WeekID, Day string }
type TrainingSetupStatus struct {
	ProgrammeCode                                                        string
	Administrator, ProgrammeAuthority, MissingSeason, CategoryConfigured bool
}
type TrainingSetupStore interface {
	ResolveContext(context.Context, uuid.UUID, TrainingSetupContext) (TrainingSetupContext, string, error)
	Capabilities(context.Context, uuid.UUID, uuid.UUID) (TrainingSetupStatus, error)
}
type PostgresTrainingSetupStore struct{ Pool structuredTrainingDB }

func (s PostgresTrainingSetupStore) Capabilities(ctx context.Context, actor, group uuid.UUID) (TrainingSetupStatus, error) {
	r, err := dbgen.New(s.Pool).TrainingGroupSetupStatus(ctx, dbgen.TrainingGroupSetupStatusParams{ActorID: actor, GroupID: group})
	return TrainingSetupStatus{ProgrammeCode: r.ProgrammeCode, Administrator: r.Administrator, ProgrammeAuthority: r.ProgrammeAuthority, MissingSeason: r.MissingSeason, CategoryConfigured: r.CategoryConfigured}, err
}
func (s PostgresTrainingSetupStore) ResolveContext(ctx context.Context, actor uuid.UUID, c TrainingSetupContext) (TrainingSetupContext, string, error) {
	if c.GroupID == "" {
		return TrainingSetupContext{}, "", nil
	}
	id, err := uuid.Parse(c.GroupID)
	if err != nil || id == uuid.Nil {
		return TrainingSetupContext{}, "", pgx.ErrNoRows
	}
	if _, err = (PostgresTrainingGroupStore{Pool: s.Pool}).GetGroup(ctx, actor, id); err != nil {
		return TrainingSetupContext{}, "", err
	}
	out := TrainingSetupContext{GroupID: id.String()}
	dest := trainingGroupsPath + "/" + out.GroupID
	if week, e := uuid.Parse(c.WeekID); e == nil && week != uuid.Nil {
		start, e := dbgen.New(s.Pool).TrainingGroupSetupWeek(ctx, dbgen.TrainingGroupSetupWeekParams{ActorID: actor, GroupID: id, WeekID: week})
		if e == nil {
			out.WeekID = week.String()
			if validWorkspaceDate(c.Day) {
				d, _ := time.Parse("2006-01-02", c.Day)
				if !d.Before(start.Time) && !d.After(start.Time.AddDate(0, 0, 6)) {
					out.Day = c.Day
				}
			}
			dest = structuredPlannerURL(out.GroupID, out.WeekID, "", out.Day)
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return out, "", e
		} else {
			// Missing/mismatched week and newly revoked group authority both produce
			// no rows. Recheck before retaining an otherwise stale group destination.
			if _, e = (PostgresTrainingGroupStore{Pool: s.Pool}).GetGroup(ctx, actor, id); e != nil {
				return TrainingSetupContext{}, "", e
			}
		}
	}
	return out, dest, nil
}
func requestSetupContext(r *http.Request) TrainingSetupContext {
	return TrainingSetupContext{GroupID: r.FormValue("origin_group"), WeekID: r.FormValue("origin_week"), Day: r.FormValue("origin_day")}
}
func resolveTrainingSetup(ctx context.Context, store TrainingSetupStore, actor uuid.UUID, c TrainingSetupContext) (TrainingSetupContext, string, error) {
	if c.GroupID == "" {
		return TrainingSetupContext{}, "", nil
	}
	if store == nil {
		return TrainingSetupContext{}, "", pgx.ErrNoRows
	}
	return store.ResolveContext(ctx, actor, c)
}
func setupContextURL(base string, c TrainingSetupContext) string {
	u, _ := url.Parse(base)
	q := u.Query()
	for key, value := range map[string]string{"origin_group": c.GroupID, "origin_week": c.WeekID, "origin_day": c.Day} {
		if value != "" {
			q.Set(key, value)
		}
	}
	u.RawQuery = q.Encode()
	return u.String()
}
func setupConfirmationBinding(actor, member uuid.UUID, kind string, value any, c TrainingSetupContext) string {
	if c.GroupID == "" {
		return confirmationBinding(actor, member, kind, value)
	}
	return confirmationBinding(actor, member, kind+":SETUP", struct {
		Value  any
		Origin TrainingSetupContext
	}{value, c})
}
func setupSeasonContext(kind, id string, c TrainingSetupContext) SeasonReturnContext {
	return SeasonReturnContext{Kind: kind, ID: id, OriginGroup: c.GroupID, OriginWeek: c.WeekID, OriginDay: c.Day}
}
func groupContextDayURL(base, week, day string) string {
	u, _ := url.Parse(base)
	q := u.Query()
	if week != "" {
		q.Set("week_id", week)
	} else {
		q.Del("week_id")
	}
	if day != "" {
		q.Set("day", day)
	} else {
		q.Del("day")
	}
	u.RawQuery = q.Encode()
	return u.String()
}
func (h TrainingGroups) applySetup(ctx context.Context, r *http.Request, actor CurrentUser, g TrainingGroupRecord, p *pages.TrainingGroupsPage) error {
	if h.Setup == nil {
		return nil
	}
	c, _, err := h.Setup.ResolveContext(ctx, actor.ID, TrainingSetupContext{GroupID: g.ID.String(), WeekID: p.WeekID, Day: r.FormValue("day")})
	if err != nil {
		return err
	}
	p.WeekID = c.WeekID
	p.ContextDay = c.Day
	base := trainingGroupsPath + "/" + g.ID.String()
	p.GroupURL = groupContextDayURL(base, c.WeekID, c.Day)
	p.Group.URL = p.GroupURL
	p.ReturnURL = p.GroupURL
	p.AddURL = groupContextDayURL(base+"/atletas", c.WeekID, c.Day)
	p.RenameURL = groupContextDayURL(base+"/nome", c.WeekID, c.Day)
	if c.WeekID != "" {
		p.PlannerURL = structuredPlannerURL(c.GroupID, c.WeekID, "", c.Day)
		p.Group.PlannerURL = p.PlannerURL
	}
	if p.PreviousURL != "" {
		p.PreviousURL = groupContextDayURL(p.PreviousURL, c.WeekID, c.Day)
	}
	if p.NextURL != "" {
		p.NextURL = groupContextDayURL(p.NextURL, c.WeekID, c.Day)
	}
	if p.RosterPreviousURL != "" {
		p.RosterPreviousURL = groupContextDayURL(p.RosterPreviousURL, c.WeekID, c.Day)
	}
	if p.RosterNextURL != "" {
		p.RosterNextURL = groupContextDayURL(p.RosterNextURL, c.WeekID, c.Day)
	}

	show := (p.Mode == "detail" && g.MemberCount == 0) || p.Mode == "add"
	p.Setup = pages.TrainingGroupSetup{Show: show}
	if !show {
		return nil
	}
	status, err := h.Setup.Capabilities(ctx, actor.ID, g.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if err != nil {
		// Optional configuration facts must not turn an authorized roster task into
		// a prerequisite. Recheck scope before retaining data after a failed lookup.
		if _, _, authErr := h.Setup.ResolveContext(ctx, actor.ID, c); authErr != nil {
			return authErr
		}
		p.Setup.Message = "Não foi possível confirmar as opções de configuração. Pode continuar a preparar o grupo e a semana e voltar a consultar esta ajuda mais tarde."
		return nil
	}
	help := pages.TrainingGroupSetup{Show: true, MissingSeason: status.MissingSeason, CategoryApplicable: status.ProgrammeCode == "Competition" || status.ProgrammeCode == "Initiation", CategoryRequired: status.ProgrammeCode == "Competition", CategoryConfigured: status.CategoryConfigured}
	help.CanClassify = status.Administrator || status.ProgrammeAuthority && !status.MissingSeason
	help.CanDefine = status.ProgrammeAuthority && help.CategoryApplicable && !status.MissingSeason
	messages := []string{}
	if status.MissingSeason {
		if status.Administrator {
			help.SeasonURL = seasonSetupURL(SeasonReturnContext{Kind: "group", ID: c.GroupID, WeekID: c.WeekID, Day: c.Day})
			messages = append(messages, "Ainda não existe uma época atual selecionada. Configure e selecione a época antes da classificação.")
		} else {
			messages = append(messages, "Ainda não existe uma época atual selecionada. Peça a um administrador que configure e selecione a época.")
		}
	}
	if !status.ProgrammeAuthority {
		messages = append(messages, "A classificação e os escalões são tarefas do programa. Peça a um administrador ou à equipa com essa autorização que reveja a configuração.")
	}
	if help.CategoryApplicable && !status.MissingSeason && !help.CategoryConfigured {
		messages = append(messages, "Ainda não existem escalões configurados para este programa na época atual. Reveja a classificação e os critérios deste programa, se necessário.")
	}
	help.Message = strings.Join(messages, " ")
	if help.CanClassify {
		help.ClassificationURL = setupContextURL("/equipa/classificacao", c)
	}
	if help.CanDefine {
		help.DefinitionsURL = setupContextURL("/equipa/escaloes", c)
	}
	p.Setup = help
	return nil
}
func (h TrainingGroups) setupGroupDestination(ctx context.Context, r *http.Request, actor CurrentUser, g TrainingGroupRecord) string {
	p := h.groupPage(r, actor, "detail", g)
	if h.Setup == nil {
		return p.GroupURL
	}
	_, dest, err := h.Setup.ResolveContext(ctx, actor.ID, TrainingSetupContext{GroupID: g.ID.String(), WeekID: p.WeekID, Day: r.FormValue("day")})
	if err != nil {
		return trainingGroupsPath + "/" + g.ID.String()
	}
	return destForGroupTask(dest, g.ID.String())
}
func destForGroupTask(dest, group string) string {
	// A group task returns to its detail while retaining the exact planner context.
	u, _ := url.Parse(dest)
	return groupContextDayURL(trainingGroupsPath+"/"+group, u.Query().Get("week_id"), u.Query().Get("day"))
}

const setupHiddenTemplate = `{{define "setup-origin"}}{{if .GroupID}}<input type="hidden" name="origin_group" value="{{.GroupID}}">{{end}}{{if .WeekID}}<input type="hidden" name="origin_week" value="{{.WeekID}}">{{end}}{{if .Day}}<input type="hidden" name="origin_day" value="{{.Day}}">{{end}}{{end}}`

func setupFailure(w http.ResponseWriter, r *http.Request, err error) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if errors.Is(err, pgx.ErrNoRows) {
		http.NotFound(w, r)
	} else {
		http.Error(w, "Serviço indisponível", http.StatusServiceUnavailable)
	}
}
func classificationPersonSetupURL(id uuid.UUID, q string, c TrainingSetupContext) string {
	q = boundedClassificationQuery(q)
	v := url.Values{}
	if q != "" {
		v.Set("q", q)
	}
	base := "/equipa/classificacao/" + id.String()
	if len(v) > 0 {
		base += "?" + v.Encode()
	}
	return setupContextURL(base, c)
}

func classificationCategorySetupMessage(options []ClassificationOption) string {
	messages := []string{}
	for _, option := range options {
		if option.Code != "Competition" && option.Code != "Initiation" {
			continue
		}
		if len(option.Categories) == 0 {
			messages = append(messages, option.Programme+" — "+option.Season+": ainda não existem escalões configurados. A equipa com autorização para o programa pode configurá-los na tarefa de escalões.")
			continue
		}
		visibleEligible, masked := false, false
		for _, category := range option.Categories {
			visibleEligible = visibleEligible || category.Eligible
			masked = masked || category.Masked
		}
		if !visibleEligible && !masked {
			messages = append(messages, option.Programme+" — "+option.Season+": nenhum dos escalões apresentados corresponde ao intervalo de nascimento desta pessoa. Reveja as definições e as regras de elegibilidade antes de continuar.")
		}
	}
	return strings.Join(messages, " ")
}

func boundedClassificationQuery(q string) string {
	q = strings.TrimSpace(q)
	if utf8.RuneCountInString(q) > classificationMaxSearchRunes {
		return ""
	}
	return q
}
func classificationCorrectionSetupURL(id uuid.UUID, q string, c TrainingSetupContext) string {
	q = boundedClassificationQuery(q)
	base := "/equipa/classificacao/" + id.String() + "/selecoes"
	if q != "" {
		base += "?" + url.Values{"q": {q}}.Encode()
	}
	return setupContextURL(base, c)
}
