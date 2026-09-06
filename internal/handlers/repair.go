package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	csrf "filippo.io/csrf/gorilla"
	"github.com/a-h/templ"
	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/storage"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const repairQueryTimeout = 5 * time.Second
const repairMultipartMemory = 1 << 20

type RepairStore interface {
	GetRepairByIdempotencyKey(context.Context, uuid.UUID) (dbgen.RepairRequest, error)
	GetEquipmentByID(context.Context, uuid.UUID) (dbgen.Equipment, error)
	CreateRepairRequest(context.Context, dbgen.CreateRepairRequestParams) (dbgen.RepairRequest, error)
	ListOperationalEquipment(context.Context, int32) ([]dbgen.Equipment, error)
	ListRepairRequestsForMembers(context.Context, dbgen.ListRepairRequestsForMembersParams) ([]dbgen.ListRepairRequestsForMembersRow, error)
}

type Repair struct {
	Store           RepairStore
	Objects         storage.ObjectStore
	Sessions        *scs.SessionManager
	MaxRequestBytes int64
	MaxPhotoBytes   int64
	Location        *time.Location
	Now             func() time.Time
	PageMeta        components.PageMeta
	System          System
}

func (h Repair) Index(w http.ResponseWriter, r *http.Request) {
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), repairQueryTimeout)
	defer cancel()
	equipment, err := h.Store.ListOperationalEquipment(ctx, 500)
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	repairs, err := h.Store.ListRepairRequestsForMembers(ctx, dbgen.ListRepairRequestsForMembersParams{UserID: user.ID, ResolvedSince: pgtype.Timestamptz{Time: h.now().AddDate(0, 0, -30), Valid: true}, RowLimit: 100})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	openRepairs, resolvedRepairs, openCounts := h.memberRepairSummaries(repairs)
	choices := repairChoices(equipment)
	for i := range choices {
		choices[i].OpenRepairs = openCounts[choices[i].ID]
	}
	meta := h.PageMeta
	meta.Title = "Frota | MyCFCoimbra"
	meta.CurrentPath = "/fleet"
	meta.CurrentUserName = user.Name
	meta.CurrentUserID = user.ID.String()
	meta.EmailVerificationPending = !user.IsDependent && !user.EmailVerified
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	success := ""
	if h.Sessions != nil {
		success = h.Sessions.PopString(r.Context(), "repair_flash")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pages.FleetReport(pages.FleetReportPage{Meta: meta, OpenRepairs: openRepairs, ResolvedRepairs: resolvedRepairs, RepairForm: components.RepairFormData{CSRFField: meta.CSRFField, IdempotencyKey: uuid.NewString(), Equipment: choices, Success: success}}).Render(r.Context(), w)
}

func (h Repair) memberRepairSummaries(items []dbgen.ListRepairRequestsForMembersRow) ([]pages.FleetRepairSummary, []pages.FleetRepairSummary, map[string]int) {
	open := make([]pages.FleetRepairSummary, 0, len(items))
	resolved := make([]pages.FleetRepairSummary, 0, len(items))
	counts := make(map[string]int)
	for _, item := range items {
		summary := pages.FleetRepairSummary{ID: item.ID.String(), EquipmentID: item.EquipmentID.String(), Equipment: item.AssetTag + " - " + item.EquipmentName, Description: item.IssueDescription, Status: item.Status, ReportedAt: item.DateReported.Time.In(h.location()).Format("02/01/2006"), ReportedByUser: item.ReportedByUser}
		if item.Status == "Resolvido" {
			if item.ResolvedAt.Valid {
				summary.ResolvedAt = item.ResolvedAt.Time.In(h.location()).Format("02/01/2006")
			}
			resolved = append(resolved, summary)
			continue
		}
		counts[summary.EquipmentID]++
		open = append(open, summary)
	}
	return open, resolved, counts
}

func (h Repair) Post(w http.ResponseWriter, r *http.Request) {
	user, ok := CurrentUserFromContext(r.Context())
	if !ok {
		http.Error(w, "Pedido recusado", http.StatusForbidden)
		return
	}
	maxRequest := h.MaxRequestBytes
	if maxRequest <= 0 {
		maxRequest = 12 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequest)
	if err := r.ParseMultipartForm(repairMultipartMemory); err != nil {
		if isTooLarge(err) {
			h.error(w, r, http.StatusRequestEntityTooLarge, "O pedido excede o tamanho máximo permitido.")
			return
		}
		h.validation(w, r, repairForm{Errors: map[string]string{"form": "Não foi possível ler o formulário."}})
		return
	}
	defer r.MultipartForm.RemoveAll()
	form, photo, err := repairRequestForm(r.MultipartForm)
	if err != nil {
		h.validation(w, r, repairForm{Errors: map[string]string{"form": err.Error()}})
		return
	}
	key, err := uuid.Parse(form.IdempotencyKey)
	if err != nil {
		form.Errors["form"] = "O pedido não é válido. Atualize a página e tente novamente."
		h.validation(w, r, form)
		return
	}
	equipmentID, err := uuid.Parse(form.EquipmentID)
	if err != nil {
		form.Errors["equipment_id"] = "Selecione um equipamento válido."
		h.validation(w, r, form)
		return
	}
	if form.Description = strings.TrimSpace(form.Description); utf8.RuneCountInString(form.Description) < 10 || utf8.RuneCountInString(form.Description) > 2000 {
		form.Errors["issue_description"] = "Descreva a avaria entre 10 e 2000 caracteres."
		h.validation(w, r, form)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), repairQueryTimeout)
	defer cancel()
	existing, err := h.Store.GetRepairByIdempotencyKey(ctx, key)
	if err == nil {
		h.existing(w, r, user, existing, form.ReturnTo)
		return
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		h.internal(w, r, err)
		return
	}
	equipment, err := h.Store.GetEquipmentByID(ctx, equipmentID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && equipment.Status == "Retired") {
		form.Errors["equipment_id"] = "Selecione um equipamento disponível."
		h.validation(w, r, form)
		return
	}
	if err != nil {
		h.internal(w, r, err)
		return
	}

	params := dbgen.CreateRepairRequestParams{IdempotencyKey: key, EquipmentID: equipmentID, ReportedByID: &user.ID, IssueDescription: form.Description}
	objectKey := ""
	if photo != nil {
		file, err := photo.Open()
		if err != nil {
			h.validation(w, r, form)
			return
		}
		validated, validationErr := storage.ValidateRepairPhoto(file, h.photoLimit())
		_ = file.Close()
		if validationErr != nil {
			form.Errors["photo"] = validationErr.Error() + " Selecione a imagem novamente."
			h.validation(w, r, form)
			return
		}
		objectKey = fmt.Sprintf("repairs/%s/%s.%s", h.now().In(h.location()).Format("2006/01"), uuid.New(), validated.Extension)
		uploadCtx := storage.WithUploadMetadata(r.Context(), storage.UploadMetadata{RequestID: httpx.RequestID(r.Context()), UserID: user.ID.String()})
		if err := h.Objects.PutObject(uploadCtx, objectKey, validated.ContentType, validated.Size, bytes.NewReader(validated.Bytes)); err != nil {
			h.internal(w, r, err)
			return
		}
		params.ImageObjectKey, params.ImageContentType, params.ImageSizeBytes = &objectKey, &validated.ContentType, &validated.Size
	}
	repair, err := h.Store.CreateRepairRequest(ctx, params)
	if err != nil {
		if objectKey != "" {
			h.deleteObject(r, objectKey)
		}
		if isUniqueViolation(err) {
			existing, lookupErr := h.Store.GetRepairByIdempotencyKey(ctx, key)
			if lookupErr == nil {
				h.existing(w, r, user, existing, form.ReturnTo)
				return
			}
		}
		h.internal(w, r, err)
		return
	}
	h.success(w, r, repair, form.ReturnTo)
}

type repairForm struct {
	IdempotencyKey, EquipmentID, Description, ReturnTo string
	Errors                                             map[string]string
}

func repairRequestForm(form *multipart.Form) (repairForm, *multipart.FileHeader, error) {
	allowed := map[string]bool{"idempotency_key": true, "equipment_id": true, "issue_description": true, "return_to": true, "gorilla.csrf.Token": true, "photo": true}
	for field, values := range form.Value {
		if !allowed[field] || len(values) != 1 {
			return repairForm{}, nil, errors.New("O formulário contém campos inválidos.")
		}
	}
	for field, files := range form.File {
		if !allowed[field] || field != "photo" || len(files) != 1 {
			return repairForm{}, nil, errors.New("O formulário contém campos inválidos.")
		}
	}
	if _, ok := form.Value["idempotency_key"]; !ok {
		return repairForm{}, nil, errors.New("O formulário contém campos inválidos.")
	}
	if _, ok := form.Value["equipment_id"]; !ok {
		return repairForm{}, nil, errors.New("O formulário contém campos inválidos.")
	}
	if _, ok := form.Value["issue_description"]; !ok {
		return repairForm{}, nil, errors.New("O formulário contém campos inválidos.")
	}
	returnTo := ""
	if values := form.Value["return_to"]; len(values) == 1 {
		if safe := safeCollectionReturn(values[0]); strings.HasPrefix(safe, "/admin/fleet") {
			returnTo = safe
		}
	}
	f := repairForm{IdempotencyKey: form.Value["idempotency_key"][0], EquipmentID: form.Value["equipment_id"][0], Description: form.Value["issue_description"][0], ReturnTo: returnTo, Errors: map[string]string{}}
	if files := form.File["photo"]; len(files) == 1 && files[0].Filename != "" {
		return f, files[0], nil
	}
	return f, nil, nil
}

func (h Repair) existing(w http.ResponseWriter, r *http.Request, user CurrentUser, repair dbgen.RepairRequest, returnTo string) {
	if repair.ReportedByID == nil || *repair.ReportedByID != user.ID {
		slog.Warn("repair idempotency key belongs to another user", "request_id", httpx.RequestID(r.Context()))
		h.error(w, r, http.StatusConflict, "Não foi possível concluir o pedido.")
		return
	}
	h.success(w, r, repair, returnTo)
}
func (h Repair) success(w http.ResponseWriter, r *http.Request, repair dbgen.RepairRequest, returnTo string) {
	if httpx.IsHTMX(r) {
		h.renderForm(w, r, http.StatusOK, repairForm{IdempotencyKey: uuid.NewString(), ReturnTo: returnTo}, "Avaria reportada. Referência: "+repair.ID.String()[:8])
		return
	}
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "repair_flash", "Avaria reportada. Referência: "+repair.ID.String()[:8])
	}
	if !strings.HasPrefix(returnTo, "/admin/fleet") {
		returnTo = "/fleet"
	}
	http.Redirect(w, r, returnTo, http.StatusSeeOther)
}
func (h Repair) validation(w http.ResponseWriter, r *http.Request, form repairForm) {
	h.renderForm(w, r, http.StatusUnprocessableEntity, form, "")
}
func (h Repair) renderForm(w http.ResponseWriter, r *http.Request, status int, form repairForm, success string) {
	equipment, err := h.Store.ListOperationalEquipment(r.Context(), 500)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	choices := repairChoices(equipment)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = components.RepairForm(components.RepairFormData{CSRFField: templ.Raw(string(csrf.TemplateField(r))), IdempotencyKey: form.IdempotencyKey, ReturnTo: form.ReturnTo, Equipment: choices, EquipmentID: form.EquipmentID, Description: form.Description, Errors: form.Errors, Success: success}).Render(r.Context(), w)
}
func repairChoices(equipment []dbgen.Equipment) []components.RepairEquipment {
	choices := make([]components.RepairEquipment, len(equipment))
	for i, item := range equipment {
		choices[i] = components.RepairEquipment{ID: item.ID.String(), Label: item.AssetTag + " - " + item.Name}
	}
	return choices
}
func (h Repair) error(w http.ResponseWriter, _ *http.Request, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, "<p role=\"alert\">%s</p>", message)
}
func (h Repair) internal(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("repair request failed", "request_id", httpx.RequestID(r.Context()), "error", err)
	h.error(w, r, http.StatusInternalServerError, "Não foi possível concluir o pedido. Referência: "+httpx.RequestID(r.Context()))
}
func (h Repair) deleteObject(r *http.Request, key string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.Objects.DeleteObject(ctx, key); err != nil {
		slog.Error("delete repair photo after database failure", "object_key", key, "request_id", httpx.RequestID(r.Context()), "error", err)
	}
}
func (h Repair) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}
func (h Repair) location() *time.Location {
	if h.Location != nil {
		return h.Location
	}
	return time.UTC
}
func (h Repair) photoLimit() int64 {
	if h.MaxPhotoBytes > 0 {
		return h.MaxPhotoBytes
	}
	return 10 << 20
}
func isTooLarge(err error) bool { var maxErr *http.MaxBytesError; return errors.As(err, &maxErr) }
