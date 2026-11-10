package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"reflect"
	"strings"
	"time"

	mycfcdb "github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type TrainingSchedulePrecision string
type TrainingDayBand string

const (
	TrainingScheduleInterval  TrainingSchedulePrecision = "INTERVAL"
	TrainingScheduleStartOnly TrainingSchedulePrecision = "START_ONLY"
	TrainingScheduleDay       TrainingSchedulePrecision = "DAY"
	TrainingScheduleBand      TrainingSchedulePrecision = "BAND"
	TrainingBandMorning       TrainingDayBand           = "MORNING"
	TrainingBandAfternoon     TrainingDayBand           = "AFTERNOON"
)

var ErrTrainingScheduleWriteUnsupported = mycfcdb.ErrTrainingScheduleWriteUnsupported
var errTrainingScheduleUnavailable = errors.New("training schedule target unavailable")
var ErrTrainingScheduleInvalid = errors.New("invalid recorded training schedule")
var ErrTrainingSnapshotUnsupported = errors.New("training snapshot version unavailable")

const trainingScheduleUnsupportedMessage = "Esta versão permite consultar este horário, mas não o pode alterar, copiar ou voltar a publicar sem perder informação. Regresse à semana e peça à equipa que use uma versão compatível. Nada foi guardado."

type TrainingSessionSchedule struct {
	Precision TrainingSchedulePrecision `json:"precision"`
	Date      string                    `json:"date"`
	StartsAt  *time.Time                `json:"starts_at"`
	EndsAt    *time.Time                `json:"ends_at"`
	Band      *TrainingDayBand          `json:"band"`
}
type TrainingScheduleSaveInput struct {
	ActorID, PlanID                                 uuid.UUID
	SessionID                                       *uuid.UUID
	ExpectedSessionUpdatedAt, ExpectedPlanUpdatedAt time.Time
	Schedule                                        TrainingSessionSchedule
}

func (s TrainingSessionSchedule) Validate() error {
	if !validWorkspaceDate(s.Date) {
		return ErrTrainingScheduleInvalid
	}
	matches := func(t *time.Time) bool {
		return t != nil && !t.IsZero() && t.In(lisbonLocation()).Format("2006-01-02") == s.Date
	}
	switch s.Precision {
	case TrainingScheduleInterval:
		if !matches(s.StartsAt) || s.EndsAt == nil || !s.EndsAt.After(*s.StartsAt) || s.Band != nil {
			return ErrTrainingScheduleInvalid
		}
	case TrainingScheduleStartOnly:
		if !matches(s.StartsAt) || s.EndsAt != nil || s.Band != nil {
			return ErrTrainingScheduleInvalid
		}
	case TrainingScheduleDay:
		if s.StartsAt != nil || s.EndsAt != nil || s.Band != nil {
			return ErrTrainingScheduleInvalid
		}
	case TrainingScheduleBand:
		if s.StartsAt != nil || s.EndsAt != nil || s.Band == nil || (*s.Band != TrainingBandMorning && *s.Band != TrainingBandAfternoon) {
			return ErrTrainingScheduleInvalid
		}
	default:
		return ErrTrainingScheduleInvalid
	}
	return nil
}
func (s TrainingSessionSchedule) ValidateKind(kind string) error {
	if kind != "TRAINING" && kind != "REST" && kind != "COMPETITION" && kind != "LOGISTICS" {
		return ErrTrainingScheduleInvalid
	}
	if e := s.Validate(); e != nil {
		return e
	}
	if (s.Precision == TrainingScheduleDay || s.Precision == TrainingScheduleBand) && kind != "REST" && kind != "LOGISTICS" {
		return ErrTrainingScheduleInvalid
	}
	return nil
}
func trainingScheduleFromRecord(precision string, date pgtype.Date, start, end pgtype.Timestamptz, band *string) (TrainingSessionSchedule, error) {
	if date.Valid && date.InfinityModifier != pgtype.Finite || start.Valid && start.InfinityModifier != pgtype.Finite || end.Valid && end.InfinityModifier != pgtype.Finite {
		return TrainingSessionSchedule{}, ErrTrainingScheduleInvalid
	}
	if precision == "" {
		precision = string(TrainingScheduleInterval)
	}
	s := TrainingSessionSchedule{Precision: TrainingSchedulePrecision(precision)}
	if date.Valid {
		s.Date = date.Time.Format("2006-01-02")
	} else if s.Precision == TrainingScheduleInterval && start.Valid {
		s.Date = start.Time.In(lisbonLocation()).Format("2006-01-02")
	}
	if start.Valid {
		value := start.Time
		s.StartsAt = &value
	}
	if end.Valid {
		value := end.Time
		s.EndsAt = &value
	}
	if band != nil {
		value := TrainingDayBand(*band)
		s.Band = &value
	}
	return s, s.Validate()
}
func trainingScheduleView(s TrainingSessionSchedule, location *time.Location) pages.TrainingScheduleView {
	if location == nil {
		location = lisbonLocation()
	}
	v := pages.TrainingScheduleView{Known: true, Precision: string(s.Precision), Date: s.Date}
	if s.Validate() != nil {
		v.Unavailable = true
		v.Label = "Não foi possível confirmar o horário guardado."
		return v
	}
	if s.StartsAt != nil {
		local := s.StartsAt.In(location)
		v.StartsOn = local.Format("2006-01-02")
		v.StartsAt = local.Format("15:04")
	}
	if s.EndsAt != nil {
		local := s.EndsAt.In(location)
		v.EndsOn = local.Format("2006-01-02")
		v.EndsAt = local.Format("15:04")
	}
	if s.Band != nil {
		v.Band = string(*s.Band)
	}
	switch s.Precision {
	case TrainingScheduleInterval:
		v.Label = v.StartsOn + " " + v.StartsAt + "–"
		if v.EndsOn != v.StartsOn {
			v.Label += v.EndsOn + " "
		}
		v.Label += v.EndsAt
	case TrainingScheduleStartOnly:
		v.Label = s.Date + " " + v.StartsAt + " · fim por definir"
	case TrainingScheduleDay:
		v.Label = s.Date + " · hora por definir"
	case TrainingScheduleBand:
		label := "Manhã"
		if *s.Band == TrainingBandAfternoon {
			label = "Tarde"
		}
		v.Label = s.Date + " · " + label + " · hora por definir"
	}

	// A known instant in a repeated local hour must not look like an invented
	// zero-length interval. Keep raw HH:MM fields stable; qualify the display label.
	if s.StartsAt != nil && (trainingAmbiguousInstant(*s.StartsAt, location) || (s.EndsAt != nil && trainingAmbiguousInstant(*s.EndsAt, location))) {
		start := s.StartsAt.In(location)
		v.Label = start.Format("2006-01-02 15:04") + " UTC" + start.Format("-07:00")
		if s.EndsAt != nil {
			end := s.EndsAt.In(location)
			v.Label += "–" + end.Format("2006-01-02 15:04") + " UTC" + end.Format("-07:00")
		} else {
			v.Label += " · fim por definir"
		}
	}
	return v
}
func trainingScheduleExact(s TrainingSessionSchedule) error {
	if e := s.Validate(); e != nil {
		return e
	}
	if s.Precision != TrainingScheduleInterval {
		return ErrTrainingScheduleWriteUnsupported
	}
	return nil
}
func trainingScheduleWriteError(err error) error {
	if err == nil {
		return nil
	}
	var pge *pgconn.PgError
	if errors.As(err, &pge) {
		if pge.Code == "40001" || pge.Code == "40P01" {
			return errStructuredTrainingPublicationConflict
		}
	}
	return mycfcdb.TrainingScheduleWriteError(err)
}

// Parse wall time without silently normalizing a gap or choosing one fold.
// C has no partial/ambiguous-time authoring control, so ambiguity requires review.
func parseTrainingLocalDateTime(raw string, location *time.Location) (time.Time, error) {
	if location == nil {
		location = lisbonLocation()
	}
	t, e := time.ParseInLocation("2006-01-02T15:04", raw, location)
	if e != nil || t.In(location).Format("2006-01-02T15:04") != raw {
		return time.Time{}, ErrTrainingScheduleInvalid
	}
	for _, shift := range []time.Duration{-2 * time.Hour, -time.Hour, time.Hour, 2 * time.Hour} {
		other := t.Add(shift)
		if other.In(location).Format("2006-01-02T15:04") == raw {
			return time.Time{}, ErrTrainingScheduleWriteUnsupported
		}
	}
	return t, nil
}
func trainingScheduleJSON(raw []byte) (TrainingSessionSchedule, error) {
	var s TrainingSessionSchedule
	fields := map[string]json.RawMessage{}
	if e := json.Unmarshal(raw, &fields); e != nil {
		return s, ErrTrainingScheduleInvalid
	}
	allowed := []string{"precision", "date", "starts_at", "ends_at", "band"}
	if len(fields) != len(allowed) {
		return s, ErrTrainingScheduleInvalid
	}
	for _, key := range allowed {
		if _, ok := fields[key]; !ok {
			return s, ErrTrainingScheduleInvalid
		}
	}
	if e := strictTrainingJSON(raw, &s); e != nil {
		return s, ErrTrainingScheduleInvalid
	}
	return s, s.Validate()
}
func strictTrainingJSON(raw []byte, value any) error {
	if len(raw) == 0 || len(raw) > 200000 {
		return ErrTrainingSnapshotUnsupported
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if e := checkTrainingJSONValue(d, 0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return ErrTrainingSnapshotUnsupported
	}
	if !exactTrainingJSONKeys(raw, reflect.TypeOf(value)) {
		return ErrTrainingSnapshotUnsupported
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(value); e != nil {
		return ErrTrainingSnapshotUnsupported
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return ErrTrainingSnapshotUnsupported
	}
	return nil
}
func checkTrainingJSONValue(d *json.Decoder, depth int) error {
	if depth > 64 {
		return ErrTrainingSnapshotUnsupported
	}
	token, e := d.Token()
	if e != nil {
		return ErrTrainingSnapshotUnsupported
	}
	delim, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, e := d.Token()
			name, ok := key.(string)
			if e != nil || !ok || seen[name] {
				return ErrTrainingSnapshotUnsupported
			}
			seen[name] = true
			if e = checkTrainingJSONValue(d, depth+1); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e = checkTrainingJSONValue(d, depth+1); e != nil {
				return e
			}
		}
	default:
		return ErrTrainingSnapshotUnsupported
	}
	_, e = d.Token()
	if e != nil {
		return ErrTrainingSnapshotUnsupported
	}
	return nil
}
func trainingArtifactVersion(raw []byte) (int, error) {
	if len(raw) == 0 || len(raw) > 200000 {
		return 0, ErrTrainingSnapshotUnsupported
	}
	var root map[string]json.RawMessage
	if e := json.Unmarshal(raw, &root); e != nil {
		return 0, ErrTrainingSnapshotUnsupported
	}
	v, ok := root["schema_version"]
	if !ok {
		return 1, nil
	}
	if bytes.Equal(bytes.TrimSpace(v), []byte("1")) {
		return 1, nil
	}
	if bytes.Equal(bytes.TrimSpace(v), []byte("2")) {
		return 2, nil
	}
	return 0, ErrTrainingSnapshotUnsupported
}
func unsupportedTrainingArtifact(raw []byte) error {
	version, e := trainingArtifactVersion(raw)
	if e != nil {
		return e
	}
	if version != 1 {
		return ErrTrainingScheduleWriteUnsupported
	}
	return nil
}
func trainingScheduleDisplayError(err error) string {
	if errors.Is(err, ErrTrainingScheduleWriteUnsupported) {
		return trainingScheduleUnsupportedMessage
	}
	return "Não foi possível ler o horário ou a versão guardada. A consulta está indisponível; regresse à tarefa e tente novamente. Nada foi alterado."
}
func trainingScheduleSortKey(s TrainingSessionSchedule) string {
	clock := ""
	if s.StartsAt != nil {
		clock = s.StartsAt.In(lisbonLocation()).Format("15:04:05.999999999")
	}
	bucket := "0"
	if s.Band != nil {
		bucket = "1"
		if *s.Band == TrainingBandAfternoon {
			bucket = "3"
		}
	} else if clock != "" {
		bucket = "2"
		if clock >= "12:00" {
			bucket = "4"
		}
	}
	return strings.Join([]string{s.Date, bucket, clock}, "/")
}

// C cannot choose between wall-clock and elapsed semantics across an offset
// change. Refuse the copy rather than silently selecting either meaning.
func trainingCopiedExactStart(source time.Time, year int, month time.Month, day int) (time.Time, error) {
	local := source.In(lisbonLocation())
	targetDate := time.Date(year, month, day, 0, 0, 0, 0, lisbonLocation())
	raw := targetDate.Format("2006-01-02") + "T" + local.Format("15:04")
	target, e := parseTrainingLocalDateTime(raw, lisbonLocation())
	if e != nil {
		return time.Time{}, e
	}
	_, sourceOffset := local.Zone()
	_, targetOffset := target.Zone()
	if sourceOffset != targetOffset {
		return time.Time{}, ErrTrainingScheduleWriteUnsupported
	}
	return target.Add(time.Duration(local.Second())*time.Second + time.Duration(local.Nanosecond())), nil
}

// encoding/json deliberately accepts case-insensitive field names. Versioned
// training artifacts instead use the documented exact field names recursively.
func exactTrainingJSONKeys(raw []byte, t reflect.Type) bool {
	if t == nil {
		return false
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == reflect.TypeOf(json.RawMessage{}) || t == reflect.TypeOf(time.Time{}) {
		return true
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return true
	}
	switch t.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil {
			return false
		}
		fields := map[string]reflect.Type{}
		var add func(reflect.Type)
		add = func(st reflect.Type) {
			for i := 0; i < st.NumField(); i++ {
				f := st.Field(i)
				tag := strings.Split(f.Tag.Get("json"), ",")[0]
				if tag == "-" || !f.IsExported() {
					continue
				}
				if f.Anonymous && tag == "" {
					nested := f.Type
					for nested.Kind() == reflect.Pointer {
						nested = nested.Elem()
					}
					if nested.Kind() == reflect.Struct {
						add(nested)
						continue
					}
				}
				if tag == "" {
					tag = f.Name
				}
				fields[tag] = f.Type
			}
		}
		add(t)
		for key, value := range object {
			ft, ok := fields[key]
			if !ok || !exactTrainingJSONKeys(value, ft) {
				return false
			}
		}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return true
		}
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil {
			return false
		}
		for _, value := range values {
			if !exactTrainingJSONKeys(value, t.Elem()) {
				return false
			}
		}
	}
	return true
}

func trainingAmbiguousInstant(t time.Time, location *time.Location) bool {
	local := t.In(location).Format("2006-01-02T15:04")
	for _, shift := range []time.Duration{-2 * time.Hour, -time.Hour, time.Hour, 2 * time.Hour} {
		if t.Add(shift).In(location).Format("2006-01-02T15:04") == local {
			return true
		}
	}
	return false
}

func trainingScheduleFormUnsupported(values url.Values) bool {
	// A P request must not become an exact/v1 write merely because it reached a
	// compatibility binary. These fields have no authoring meaning in C.
	for _, key := range []string{"precision", "schedule_precision", "scheduled_on", "day_band", "schema_version", "schedule"} {
		if strings.TrimSpace(values.Get(key)) != "" {
			return true
		}
	}
	return false
}

// Review a proposed exact copy against BOTH known endpoint offsets. C cannot
// choose a wall-clock/elapsed result when either boundary's offset pattern
// changes. A legacy routine with only recorded elapsed duration is separate.
func trainingCopiedExactInterval(sourceStart, sourceEnd, targetStart time.Time) error {
	if sourceStart.IsZero() || !sourceEnd.After(sourceStart) || targetStart.IsZero() {
		return ErrTrainingScheduleInvalid
	}
	targetEnd := targetStart.Add(sourceEnd.Sub(sourceStart))
	_, ss := sourceStart.In(lisbonLocation()).Zone()
	_, se := sourceEnd.In(lisbonLocation()).Zone()
	_, ts := targetStart.In(lisbonLocation()).Zone()
	_, te := targetEnd.In(lisbonLocation()).Zone()
	if ss != ts || se != te {
		return ErrTrainingScheduleWriteUnsupported
	}
	return nil
}
