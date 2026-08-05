package handlers

import (
	"bytes"
	"context"
	"encoding/json"
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
	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/cfcoimbra/mycfc/internal/storage"
	"github.com/cfcoimbra/mycfc/internal/validation"
	"github.com/cfcoimbra/mycfc/ui/components"
	"github.com/cfcoimbra/mycfc/ui/pages"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const equipmentAuditLimit = 100

type equipmentForm struct {
	ID, AssetTag, Name, Type, Status, Notes, ExpectedUpdatedAt string
	PhotoURL, PhotoUnavailable                                 string
	ImageObjectKey, ImageContentType                           *string
	HasPhoto                                                   bool
	Retired                                                    bool
	Errors                                                     validation.FieldErrors
}

type equipmentSnapshot struct {
	AssetTag       string  `json:"asset_tag"`
	Name           string  `json:"name"`
	Type           string  `json:"type"`
	Status         string  `json:"status"`
	Notes          string  `json:"notes"`
	ImageObjectKey *string `json:"image_object_key"`
}

func (h Dashboard) CreateEquipment(w http.ResponseWriter, r *http.Request) {
	photo, err := h.parseEquipmentForm(w, r)
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		h.renderFleetEquipment(w, r, http.StatusBadRequest, equipmentForm{Type: "Boat", Status: "Operational", Errors: validation.FieldErrors{}})
		return
	}
	form := validateEquipmentForm(r, false)
	if !form.Errors.Empty() {
		h.renderFleetEquipment(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	validated, ok := h.validateEquipmentPhoto(w, r, form, photo, false)
	if !ok {
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	objectKey, contentType, size, ok := h.uploadEquipmentPhoto(r, user, validated)
	if !ok {
		h.System.InternalError(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	_, err = h.Equipment.CreateEquipmentWithAudit(ctx, dbgen.CreateEquipmentWithAuditParams{AssetTag: form.AssetTag, Name: form.Name, Type: form.Type, Status: form.Status, Notes: form.Notes, ImageObjectKey: objectKey, ImageContentType: contentType, ImageSizeBytes: size, ActorUserID: user.ID})
	if isUniqueViolation(err) {
		h.deleteEquipmentObject(r, objectKey)
		form.Errors.Add("asset_tag", "Já existe um equipamento com este identificador.")
		h.renderFleetEquipment(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	if err != nil {
		h.deleteEquipmentObject(r, objectKey)
		h.System.InternalError(w, r)
		return
	}
	h.fleetFlash(r, "Equipamento adicionado.")
	httpx.Redirect(w, r, "/admin/fleet", http.StatusSeeOther)
}

func (h Dashboard) EditEquipment(w http.ResponseWriter, r *http.Request) {
	id, ok := h.equipmentID(w, r)
	if !ok {
		return
	}
	equipment, err := h.getEquipment(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	h.renderEquipmentEdit(w, r, http.StatusOK, equipmentFormFromModel(equipment), "")
}

func (h Dashboard) UpdateEquipment(w http.ResponseWriter, r *http.Request) {
	id, ok := h.equipmentID(w, r)
	if !ok {
		return
	}
	photo, err := h.parseEquipmentForm(w, r)
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	current, err := h.getEquipment(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	form := validateEquipmentForm(r, current.Status == "Retired")
	form.ID = id.String()
	form.Retired = current.Status == "Retired"
	form.HasPhoto, form.ImageObjectKey, form.ImageContentType = current.ImageObjectKey != nil, current.ImageObjectKey, current.ImageContentType
	if form.Retired {
		form.Status = "Retired"
	}
	if !form.Errors.Empty() {
		h.renderEquipmentEdit(w, r, http.StatusUnprocessableEntity, form, "")
		return
	}
	expected, err := time.Parse(time.RFC3339Nano, form.ExpectedUpdatedAt)
	if err != nil || !current.UpdatedAt.Valid || !current.UpdatedAt.Time.Equal(expected) {
		h.renderEquipmentEdit(w, r, http.StatusConflict, equipmentFormFromModel(current), "O equipamento foi alterado entretanto. Reveja os valores atuais e volte a aplicar a alteração.")
		return
	}
	validated, ok := h.validateEquipmentPhoto(w, r, form, photo, true)
	if !ok {
		return
	}
	if equipmentUnchanged(current, form) && validated == nil {
		h.fleetFlash(r, "Não existiam alterações para guardar.")
		httpx.Redirect(w, r, "/admin/fleet", http.StatusSeeOther)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	imageKey, imageType, imageSize := current.ImageObjectKey, current.ImageContentType, current.ImageSizeBytes
	newKey, newType, newSize, ok := h.uploadEquipmentPhoto(r, user, validated)
	if !ok {
		h.System.InternalError(w, r)
		return
	}
	if newKey != nil {
		imageKey, imageType, imageSize = newKey, newType, newSize
	}
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	_, err = h.Equipment.UpdateEquipmentWithAudit(ctx, dbgen.UpdateEquipmentWithAuditParams{EquipmentID: id, ExpectedUpdatedAt: pgtype.Timestamptz{Time: expected, Valid: true}, AssetTag: form.AssetTag, Name: form.Name, Type: form.Type, Status: form.Status, Notes: form.Notes, ImageObjectKey: imageKey, ImageContentType: imageType, ImageSizeBytes: imageSize, ActorUserID: user.ID})
	if isUniqueViolation(err) {
		h.deleteEquipmentObject(r, newKey)
		form.Errors.Add("asset_tag", "Já existe um equipamento com este identificador.")
		h.renderEquipmentEdit(w, r, http.StatusUnprocessableEntity, form, "")
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		h.deleteEquipmentObject(r, newKey)
		latest, getErr := h.getEquipment(r.Context(), id)
		if getErr != nil {
			h.System.InternalError(w, r)
			return
		}
		h.renderEquipmentEdit(w, r, http.StatusConflict, equipmentFormFromModel(latest), "O equipamento foi alterado entretanto. Reveja os valores atuais e volte a aplicar a alteração.")
		return
	}
	if err != nil {
		h.deleteEquipmentObject(r, newKey)
		h.System.InternalError(w, r)
		return
	}
	if newKey != nil && current.ImageObjectKey != nil {
		h.deleteEquipmentObject(r, current.ImageObjectKey)
	}
	h.fleetFlash(r, "Equipamento atualizado.")
	httpx.Redirect(w, r, "/admin/fleet", http.StatusSeeOther)
}

func (h Dashboard) RetireEquipment(w http.ResponseWriter, r *http.Request) {
	h.changeEquipmentLifecycle(w, r, true)
}

func (h Dashboard) ReactivateEquipment(w http.ResponseWriter, r *http.Request) {
	h.changeEquipmentLifecycle(w, r, false)
}

func (h Dashboard) changeEquipmentLifecycle(w http.ResponseWriter, r *http.Request, retire bool) {
	id, ok := h.equipmentID(w, r)
	if !ok {
		return
	}
	if _, err := h.getEquipment(r.Context(), id); errors.Is(err, pgx.ErrNoRows) {
		h.System.NotFound(w, r)
		return
	} else if err != nil {
		h.System.InternalError(w, r)
		return
	}
	user, _ := CurrentUserFromContext(r.Context())
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	var err error
	if retire {
		_, err = h.Equipment.RetireEquipmentWithAudit(ctx, dbgen.RetireEquipmentWithAuditParams{EquipmentID: id, ActorUserID: user.ID})
	} else {
		_, err = h.Equipment.ReactivateEquipmentWithAudit(ctx, dbgen.ReactivateEquipmentWithAuditParams{EquipmentID: id, ActorUserID: user.ID})
	}
	if errors.Is(err, pgx.ErrNoRows) {
		http.Error(w, "A alteração de estado já não é válida.", http.StatusConflict)
		return
	}
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	message := "Equipamento retirado da frota. As manutenções ativas foram canceladas."
	if !retire {
		message = "Equipamento reativado como operacional."
	}
	h.fleetFlash(r, message)
	httpx.Redirect(w, r, "/admin/fleet", http.StatusSeeOther)
}

func validateEquipmentForm(r *http.Request, allowRetired bool) equipmentForm {
	f := equipmentForm{AssetTag: strings.TrimSpace(r.PostForm.Get("asset_tag")), Name: strings.TrimSpace(r.PostForm.Get("name")), Type: strings.TrimSpace(r.PostForm.Get("type")), Status: strings.TrimSpace(r.PostForm.Get("status")), Notes: strings.TrimSpace(r.PostForm.Get("notes")), ExpectedUpdatedAt: strings.TrimSpace(r.PostForm.Get("expected_updated_at")), Errors: validation.FieldErrors{}}
	if n := utf8.RuneCountInString(f.AssetTag); n < 2 || n > 40 {
		f.Errors.Add("asset_tag", "O identificador deve ter entre 2 e 40 caracteres.")
	}
	if n := utf8.RuneCountInString(f.Name); n < 2 || n > 120 {
		f.Errors.Add("name", "O nome deve ter entre 2 e 120 caracteres.")
	}
	if f.Type != "Boat" && f.Type != "Paddle" && f.Type != "Vehicle" {
		f.Errors.Add("type", "Selecione um tipo de equipamento válido.")
	}
	validStatus := f.Status == "Operational" || f.Status == "Maintenance" || (allowRetired && f.Status == "Retired")
	if !validStatus {
		f.Errors.Add("status", "Selecione um estado válido.")
	}
	if utf8.RuneCountInString(f.Notes) > 4000 {
		f.Errors.Add("notes", "As notas não podem exceder 4000 caracteres.")
	}
	return f
}

func equipmentFormFromModel(e dbgen.Equipment) equipmentForm {
	return equipmentForm{ID: e.ID.String(), AssetTag: e.AssetTag, Name: e.Name, Type: e.Type, Status: e.Status, Notes: e.Notes, ExpectedUpdatedAt: e.UpdatedAt.Time.Format(time.RFC3339Nano), HasPhoto: e.ImageObjectKey != nil, ImageObjectKey: e.ImageObjectKey, ImageContentType: e.ImageContentType, Retired: e.Status == "Retired", Errors: validation.FieldErrors{}}
}

func equipmentUnchanged(e dbgen.Equipment, f equipmentForm) bool {
	return e.AssetTag == f.AssetTag && e.Name == f.Name && e.Type == f.Type && e.Status == f.Status && e.Notes == f.Notes
}

func (h Dashboard) parseEquipmentForm(w http.ResponseWriter, r *http.Request) (*multipart.FileHeader, error) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		return nil, r.ParseForm()
	}
	max := h.MaxRequestBytes
	if max <= 0 {
		max = 12 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, max)
	if err := r.ParseMultipartForm(repairMultipartMemory); err != nil {
		return nil, err
	}
	files := r.MultipartForm.File["photo"]
	if len(files) == 0 || files[0].Filename == "" {
		return nil, nil
	}
	if len(files) != 1 {
		return nil, errors.New("invalid photo field")
	}
	return files[0], nil
}

func (h Dashboard) validateEquipmentPhoto(w http.ResponseWriter, r *http.Request, form equipmentForm, header *multipart.FileHeader, edit bool) (*storage.ValidatedPhoto, bool) {
	if header == nil {
		return nil, true
	}
	file, err := header.Open()
	if err != nil {
		form.Errors.Add("photo", "Não foi possível ler a fotografia.")
		h.renderEquipmentPhotoError(w, r, form, edit)
		return nil, false
	}
	validated, err := storage.ValidateRepairPhoto(file, h.equipmentPhotoLimit())
	_ = file.Close()
	if err != nil {
		form.Errors.Add("photo", err.Error()+" Selecione a imagem novamente.")
		h.renderEquipmentPhotoError(w, r, form, edit)
		return nil, false
	}
	return &validated, true
}

func (h Dashboard) renderEquipmentPhotoError(w http.ResponseWriter, r *http.Request, form equipmentForm, edit bool) {
	if edit {
		h.renderEquipmentEdit(w, r, http.StatusUnprocessableEntity, form, "")
	} else {
		h.renderFleetEquipment(w, r, http.StatusUnprocessableEntity, form)
	}
}

func (h Dashboard) uploadEquipmentPhoto(r *http.Request, user CurrentUser, photo *storage.ValidatedPhoto) (*string, *string, *int64, bool) {
	if photo == nil {
		return nil, nil, nil, true
	}
	if h.Objects == nil {
		return nil, nil, nil, false
	}
	key := fmt.Sprintf("equipment/%s/%s.%s", h.now().In(h.location()).Format("2006/01"), uuid.New(), photo.Extension)
	ctx := storage.WithUploadMetadata(r.Context(), storage.UploadMetadata{RequestID: httpx.RequestID(r.Context()), UserID: user.ID.String()})
	if err := h.Objects.PutRepairPhoto(ctx, key, photo.ContentType, photo.Size, bytes.NewReader(photo.Bytes)); err != nil {
		return nil, nil, nil, false
	}
	return &key, &photo.ContentType, &photo.Size, true
}

func (h Dashboard) deleteEquipmentObject(r *http.Request, key *string) {
	if key == nil || h.Objects == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := h.Objects.DeleteObject(ctx, *key); err != nil {
		slog.Error("delete equipment photo", "object_key", *key, "request_id", httpx.RequestID(r.Context()), "error", err)
	}
}

func (h Dashboard) equipmentPhotoLimit() int64 {
	if h.MaxPhotoBytes > 0 {
		return h.MaxPhotoBytes
	}
	return 10 << 20
}

func (h Dashboard) getEquipment(parent context.Context, id uuid.UUID) (dbgen.Equipment, error) {
	ctx, cancel := context.WithTimeout(parent, dashboardQueryTimeout)
	defer cancel()
	return h.Equipment.GetEquipmentByID(ctx, id)
}

func (h Dashboard) equipmentID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.System.NotFound(w, r)
		return uuid.Nil, false
	}
	return id, true
}

func (h Dashboard) renderEquipmentEdit(w http.ResponseWriter, r *http.Request, status int, form equipmentForm, conflict string) {
	id, err := uuid.Parse(form.ID)
	if err != nil {
		h.System.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	events, err := h.Equipment.ListEquipmentAuditEvents(ctx, dbgen.ListEquipmentAuditEventsParams{EquipmentID: id, RowLimit: equipmentAuditLimit})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	form.PhotoURL, form.PhotoUnavailable = h.equipmentPhotoURL(ctx, r, form.ImageObjectKey, form.ImageContentType, id)
	page := pages.EquipmentEditPage{Meta: h.equipmentMeta(r), Form: equipmentPageForm(form), Conflict: conflict, Audit: equipmentAuditItems(events, h.location())}
	page.Form.CSRFField = page.Meta.CSRFField
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.EquipmentEdit(page).Render(r.Context(), w)
}

func (h Dashboard) renderFleetEquipment(w http.ResponseWriter, r *http.Request, status int, form equipmentForm) {
	ctx, cancel := context.WithTimeout(r.Context(), dashboardQueryTimeout)
	defer cancel()
	page, err := h.fleetPage(ctx, r, fleetMaintenanceForm{})
	if err != nil {
		h.System.InternalError(w, r)
		return
	}
	page.Meta = h.equipmentMeta(r)
	page.Meta.CurrentPath = "/admin/fleet"
	page.EquipmentForm = equipmentPageForm(form)
	page.EquipmentForm.CSRFField = page.Meta.CSRFField
	page.MaintenanceForm.CSRFField = page.Meta.CSRFField
	page.RepairForm.CSRFField = page.Meta.CSRFField
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = pages.Fleet(page).Render(r.Context(), w)
}

func equipmentPageForm(f equipmentForm) pages.EquipmentForm {
	return pages.EquipmentForm{ID: f.ID, AssetTag: f.AssetTag, Name: f.Name, Type: f.Type, Status: f.Status, Notes: f.Notes, ExpectedUpdatedAt: f.ExpectedUpdatedAt, PhotoURL: f.PhotoURL, PhotoUnavailable: f.PhotoUnavailable, HasPhoto: f.HasPhoto, Retired: f.Retired, Errors: f.Errors}
}

func (h Dashboard) equipmentPhotoURL(ctx context.Context, r *http.Request, key, contentType *string, id uuid.UUID) (string, string) {
	if key == nil {
		return "", ""
	}
	if contentType == nil || !validRepairImageContentType(*contentType) || h.Objects == nil {
		return "", "Fotografia temporariamente indisponível"
	}
	url, err := h.Objects.PresignGet(storage.WithPresignContentType(ctx, *contentType), *key, 10*time.Minute)
	if err != nil {
		slog.Warn("presign equipment photo", "equipment_id", id, "request_id", httpx.RequestID(r.Context()), "error", err)
		return "", "Fotografia temporariamente indisponível"
	}
	return url, ""
}

func (h Dashboard) equipmentMeta(r *http.Request) components.PageMeta {
	user, _ := CurrentUserFromContext(r.Context())
	meta := h.PageMeta
	meta.Title = "Equipamento | MyCFC"
	meta.CurrentPath = "/admin/fleet"
	meta.CurrentUserName = user.Name
	meta.Navigation = dashboardNavigation(user)
	meta.CSRFField = templ.Raw(string(csrf.TemplateField(r)))
	return meta
}

func (h Dashboard) fleetFlash(r *http.Request, message string) {
	if h.Sessions != nil {
		h.Sessions.Put(r.Context(), "equipment_flash", message)
	}
}

func equipmentAuditItems(rows []dbgen.ListEquipmentAuditEventsRow, location *time.Location) []pages.EquipmentAuditItem {
	items := make([]pages.EquipmentAuditItem, 0, len(rows))
	for _, row := range rows {
		var before, after equipmentSnapshot
		_ = json.Unmarshal(row.BeforeState, &before)
		_ = json.Unmarshal(row.AfterState, &after)
		items = append(items, pages.EquipmentAuditItem{Action: equipmentAuditAction(row.Action), Actor: row.ActorName, OccurredAt: row.OccurredAt.Time.In(location).Format("02/01/2006 15:04"), Changes: equipmentChanges(row.Action, before, after), CancelledMaintenance: len(row.AffectedMaintenanceIds)})
	}
	return items
}

func equipmentAuditAction(action string) string {
	switch action {
	case "CREATED":
		return "Equipamento criado"
	case "UPDATED":
		return "Equipamento atualizado"
	case "RETIRED":
		return "Equipamento retirado"
	case "REACTIVATED":
		return "Equipamento reativado"
	default:
		return action
	}
}

func equipmentChanges(action string, before, after equipmentSnapshot) []string {
	if action == "CREATED" {
		return []string{fmt.Sprintf("Criado como %s · %s.", after.AssetTag, after.Name)}
	}
	changes := []string{}
	add := func(label, old, next string) {
		if old != next {
			changes = append(changes, fmt.Sprintf("%s: %s → %s", label, old, next))
		}
	}
	add("Identificador", before.AssetTag, after.AssetTag)
	add("Nome", before.Name, after.Name)
	add("Tipo", equipmentTypeName(before.Type), equipmentTypeName(after.Type))
	add("Estado", equipmentStatusName(before.Status), equipmentStatusName(after.Status))
	add("Notas", before.Notes, after.Notes)
	if !sameOptionalString(before.ImageObjectKey, after.ImageObjectKey) {
		changes = append(changes, "Fotografia atualizada")
	}
	return changes
}

func sameOptionalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func equipmentTypeName(value string) string {
	switch value {
	case "Boat":
		return "Embarcação"
	case "Paddle":
		return "Pagaia"
	case "Vehicle":
		return "Veículo"
	default:
		return value
	}
}

func equipmentStatusName(value string) string {
	switch value {
	case "Operational":
		return "Operacional"
	case "Maintenance":
		return "Manutenção"
	case "Retired":
		return "Retirado"
	default:
		return value
	}
}
