package handlers

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type equipmentStoreFake struct {
	equipment        dbgen.Equipment
	getErr           error
	createParams     dbgen.CreateEquipmentWithAuditParams
	createErr        error
	updateParams     dbgen.UpdateEquipmentWithAuditParams
	updateErr        error
	retireParams     dbgen.RetireEquipmentWithAuditParams
	retireErr        error
	reactivateParams dbgen.ReactivateEquipmentWithAuditParams
	reactivateErr    error
	audit            []dbgen.ListEquipmentAuditEventsRow
}

func (f *equipmentStoreFake) GetEquipmentByID(context.Context, uuid.UUID) (dbgen.Equipment, error) {
	return f.equipment, f.getErr
}
func (f *equipmentStoreFake) CreateEquipmentWithAudit(_ context.Context, p dbgen.CreateEquipmentWithAuditParams) (dbgen.CreateEquipmentWithAuditRow, error) {
	f.createParams = p
	return dbgen.CreateEquipmentWithAuditRow{}, f.createErr
}
func (f *equipmentStoreFake) UpdateEquipmentWithAudit(_ context.Context, p dbgen.UpdateEquipmentWithAuditParams) (dbgen.UpdateEquipmentWithAuditRow, error) {
	f.updateParams = p
	return dbgen.UpdateEquipmentWithAuditRow{}, f.updateErr
}
func (f *equipmentStoreFake) RetireEquipmentWithAudit(_ context.Context, p dbgen.RetireEquipmentWithAuditParams) (dbgen.RetireEquipmentWithAuditRow, error) {
	f.retireParams = p
	return dbgen.RetireEquipmentWithAuditRow{}, f.retireErr
}
func (f *equipmentStoreFake) ReactivateEquipmentWithAudit(_ context.Context, p dbgen.ReactivateEquipmentWithAuditParams) (dbgen.ReactivateEquipmentWithAuditRow, error) {
	f.reactivateParams = p
	return dbgen.ReactivateEquipmentWithAuditRow{}, f.reactivateErr
}
func (f *equipmentStoreFake) ListEquipmentAuditEvents(context.Context, dbgen.ListEquipmentAuditEventsParams) ([]dbgen.ListEquipmentAuditEventsRow, error) {
	return f.audit, nil
}

func TestValidateEquipmentForm(t *testing.T) {
	tests := []struct {
		name         string
		values       url.Values
		allowRetired bool
		wantErrors   []string
	}{
		{name: "valid", values: url.Values{"asset_tag": {"  B-01  "}, "name": {"  K1 competição  "}, "type": {"Boat"}, "status": {"Operational"}, "notes": {"  Casco azul  "}}},
		{name: "invalid boundaries and enums", values: url.Values{"asset_tag": {"A"}, "name": {"N"}, "type": {"Other"}, "status": {"Retired"}, "notes": {strings.Repeat("x", 4001)}}, wantErrors: []string{"asset_tag", "name", "type", "status", "notes"}},
		{name: "retired edit", values: url.Values{"asset_tag": {"B-01"}, "name": {"K1"}, "type": {"Boat"}, "status": {"Retired"}}, allowRetired: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.values.Encode()))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			form := validateEquipmentForm(r, tc.allowRetired)
			if len(form.Errors) != len(tc.wantErrors) {
				t.Fatalf("errors = %#v", form.Errors)
			}
			for _, field := range tc.wantErrors {
				if form.Errors[field] == "" {
					t.Errorf("missing %s error", field)
				}
			}
			if tc.name == "valid" && (form.AssetTag != "B-01" || form.Name != "K1 competição" || form.Notes != "Casco azul") {
				t.Fatalf("form not trimmed: %#v", form)
			}
		})
	}
}

func TestCreateEquipmentPersistsActorAndRedirects(t *testing.T) {
	store := &equipmentStoreFake{}
	h := Dashboard{Equipment: store}
	userID := uuid.New()
	r := equipmentRequest(http.MethodPost, "/admin/fleet/equipment", url.Values{"asset_tag": {"B-01"}, "name": {"K1 competição"}, "type": {"Boat"}, "status": {"Maintenance"}, "notes": {"Casco azul"}}, userID)
	w := httptest.NewRecorder()
	h.CreateEquipment(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/fleet#equipment-inventory" {
		t.Fatalf("response = %d %q", w.Code, w.Header().Get("Location"))
	}
	if store.createParams.ActorUserID != userID || store.createParams.AssetTag != "B-01" || store.createParams.Status != "Maintenance" {
		t.Fatalf("params = %#v", store.createParams)
	}
}

func TestCreateEquipmentPreservesValidatedFleetReturn(t *testing.T) {
	store := &equipmentStoreFake{}
	h := Dashboard{Equipment: store}
	returnTo := "/admin/fleet?equipment_page=2&repairs_page=3#equipment-inventory"
	r := equipmentRequest(http.MethodPost, "/admin/fleet/equipment?return_to="+url.QueryEscape(returnTo), url.Values{"asset_tag": {"B-03"}, "name": {"K1 contexto"}, "type": {"Boat"}, "status": {"Operational"}}, uuid.New())
	w := httptest.NewRecorder()
	h.CreateEquipment(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != returnTo {
		t.Fatalf("response = %d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestCreateEquipmentUploadsValidatedPhoto(t *testing.T) {
	store := &equipmentStoreFake{}
	objects := &repairObjectStoreFake{}
	h := Dashboard{Equipment: store, Objects: objects}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range map[string]string{"asset_tag": "B-02", "name": "K2 fotografia", "type": "Boat", "status": "Operational", "notes": "Azul"} {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("photo", "barco.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(pngPhoto(t)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/admin/fleet/equipment", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	actor := uuid.New()
	r = r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor, IsAdmin: true}))
	w := httptest.NewRecorder()
	h.CreateEquipment(w, r)
	if w.Code != http.StatusSeeOther || objects.puts != 1 || store.createParams.ImageObjectKey == nil || store.createParams.ImageContentType == nil || *store.createParams.ImageContentType != "image/png" || store.createParams.ImageSizeBytes == nil {
		t.Fatalf("response=%d puts=%d params=%#v", w.Code, objects.puts, store.createParams)
	}
}

func TestEquipmentPhotoValidationRendersRecoverableCreateAndEditErrors(t *testing.T) {
	id, actor := uuid.New(), uuid.New()
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	request := func(path string, values map[string]string) *http.Request {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for key, value := range values {
			if err := writer.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
		part, err := writer.CreateFormFile("photo", "not-an-image.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte("not an image")); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, path, &body)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: actor, Name: "Administradora", IsAdmin: true}))
	}

	t.Run("create", func(t *testing.T) {
		store, fleet := &equipmentStoreFake{}, &dashboardStoreFake{}
		w := httptest.NewRecorder()
		(Dashboard{Store: fleet, Fleet: fleet, Equipment: store}).CreateEquipment(w, request("/admin/fleet/equipment", map[string]string{"asset_tag": "B-04", "name": "K1 foto", "type": "Boat", "status": "Operational"}))
		if w.Code != http.StatusUnprocessableEntity || store.createParams.AssetTag != "" || !strings.Contains(w.Body.String(), "Selecione a imagem novamente") {
			t.Fatalf("response=%d create=%#v body=%s", w.Code, store.createParams, w.Body.String())
		}
	})

	t.Run("edit", func(t *testing.T) {
		store, fleet := &equipmentStoreFake{equipment: dbgen.Equipment{ID: id, AssetTag: "B-04", Name: "K1 foto", Type: "Boat", Status: "Operational", UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}}, &dashboardStoreFake{}
		w := httptest.NewRecorder()
		r := request("/admin/fleet/equipment/"+id.String(), map[string]string{"asset_tag": "B-04", "name": "K1 foto", "type": "Boat", "status": "Operational", "expected_updated_at": now.Format(time.RFC3339Nano)})
		r.SetPathValue("id", id.String())
		(Dashboard{Store: fleet, Fleet: fleet, Equipment: store}).UpdateEquipment(w, r)
		if w.Code != http.StatusUnprocessableEntity || store.updateParams.EquipmentID != uuid.Nil || !strings.Contains(w.Body.String(), "Selecione a imagem novamente") {
			t.Fatalf("response=%d update=%#v body=%s", w.Code, store.updateParams, w.Body.String())
		}
	})
}

func TestEquipmentPhotoURLUsesOnlyValidSignedPrivateImages(t *testing.T) {
	key, png, invalid := "equipment/k1.png", "image/png", "text/plain"
	request := httptest.NewRequest(http.MethodGet, "/admin/fleet", nil)
	for _, tc := range []struct {
		name         string
		key, content *string
		objects      *profileObjectStoreFake
		wantURL      string
		unavailable  bool
	}{
		{name: "no photo"},
		{name: "invalid content type", key: &key, content: &invalid, unavailable: true},
		{name: "no object store", key: &key, content: &png, unavailable: true},
		{name: "signed", key: &key, content: &png, objects: &profileObjectStoreFake{presignedURL: "https://storage.example/k1"}, wantURL: "https://storage.example/k1"},
		{name: "signing failure", key: &key, content: &png, objects: &profileObjectStoreFake{presignErr: errors.New("storage unavailable")}, unavailable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := Dashboard{}
			if tc.objects != nil {
				h.Objects = tc.objects
			}
			url, notice := h.equipmentPhotoURL(context.Background(), request, tc.key, tc.content, uuid.New())
			if url != tc.wantURL || (notice != "") != tc.unavailable {
				t.Fatalf("url=%q notice=%q", url, notice)
			}
		})
	}
}

func TestCreateEquipmentRendersDuplicateAssetTag(t *testing.T) {
	store := &equipmentStoreFake{createErr: &pgconn.PgError{Code: "23505"}}
	fleet := &dashboardStoreFake{}
	h := Dashboard{Store: fleet, Fleet: fleet, Equipment: store}
	r := equipmentRequest(http.MethodPost, "/admin/fleet/equipment", url.Values{"asset_tag": {"B-01"}, "name": {"K1 competição"}, "type": {"Boat"}, "status": {"Operational"}}, uuid.New())
	w := httptest.NewRecorder()
	h.CreateEquipment(w, r)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "Já existe um equipamento") || !strings.Contains(w.Body.String(), `value="B-01"`) {
		t.Fatalf("response = %d %s", w.Code, w.Body.String())
	}
}

func TestUpdateEquipmentRejectsStaleVersionWithCurrentValues(t *testing.T) {
	id := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	store := &equipmentStoreFake{equipment: dbgen.Equipment{ID: id, AssetTag: "CURRENT", Name: "Nome atual", Type: "Boat", Status: "Operational", UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}}
	h := Dashboard{Equipment: store, Location: time.UTC}
	r := equipmentRequest(http.MethodPost, "/admin/fleet/equipment/"+id.String(), url.Values{"asset_tag": {"OLD"}, "name": {"Nome antigo"}, "type": {"Boat"}, "status": {"Operational"}, "expected_updated_at": {now.Add(-time.Minute).Format(time.RFC3339Nano)}}, uuid.New())
	r.SetPathValue("id", id.String())
	w := httptest.NewRecorder()
	h.UpdateEquipment(w, r)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "alterado entretanto") || !strings.Contains(w.Body.String(), `value="CURRENT"`) {
		t.Fatalf("response = %d %s", w.Code, w.Body.String())
	}
}

func TestUpdateEquipmentNoOpDoesNotWriteAudit(t *testing.T) {
	id := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	store := &equipmentStoreFake{equipment: dbgen.Equipment{ID: id, AssetTag: "B-01", Name: "K1", Type: "Boat", Status: "Operational", Notes: "Azul", UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}}
	h := Dashboard{Equipment: store}
	r := equipmentRequest(http.MethodPost, "/admin/fleet/equipment/"+id.String(), url.Values{"asset_tag": {"B-01"}, "name": {"K1"}, "type": {"Boat"}, "status": {"Operational"}, "notes": {"Azul"}, "expected_updated_at": {now.Format(time.RFC3339Nano)}}, uuid.New())
	r.SetPathValue("id", id.String())
	w := httptest.NewRecorder()
	h.UpdateEquipment(w, r)
	if w.Code != http.StatusSeeOther || store.updateParams.EquipmentID != uuid.Nil {
		t.Fatalf("response=%d params=%#v", w.Code, store.updateParams)
	}
}

func TestEquipmentEditAndUpdateRenderCurrentRecordAndWriteAuditedChange(t *testing.T) {
	id, actor := uuid.New(), uuid.New()
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	store := &equipmentStoreFake{equipment: dbgen.Equipment{ID: id, AssetTag: "B-01", Name: "K1 atual", Type: "Boat", Status: "Operational", Notes: "Casco azul", UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}}
	h := Dashboard{Equipment: store, Location: time.UTC}

	edit := equipmentRequest(http.MethodGet, "/admin/fleet/equipment/"+id.String(), nil, actor)
	edit.SetPathValue("id", id.String())
	editResponse := httptest.NewRecorder()
	h.EditEquipment(editResponse, edit)
	if editResponse.Code != http.StatusOK || !strings.Contains(editResponse.Body.String(), `value="B-01"`) || !strings.Contains(editResponse.Body.String(), "K1 atual") {
		t.Fatalf("edit = %d %s", editResponse.Code, editResponse.Body.String())
	}

	update := equipmentRequest(http.MethodPost, "/admin/fleet/equipment/"+id.String(), url.Values{"asset_tag": {"B-01"}, "name": {"K1 revisto"}, "type": {"Boat"}, "status": {"Maintenance"}, "notes": {"Vistoria concluída"}, "expected_updated_at": {now.Format(time.RFC3339Nano)}}, actor)
	update.SetPathValue("id", id.String())
	updateResponse := httptest.NewRecorder()
	h.UpdateEquipment(updateResponse, update)
	if updateResponse.Code != http.StatusSeeOther || updateResponse.Header().Get("Location") != "/admin/fleet#equipment-"+id.String() {
		t.Fatalf("update = %d %q", updateResponse.Code, updateResponse.Header().Get("Location"))
	}
	p := store.updateParams
	if p.EquipmentID != id || p.ActorUserID != actor || p.Name != "K1 revisto" || p.Status != "Maintenance" || p.Notes != "Vistoria concluída" || !p.ExpectedUpdatedAt.Valid || !p.ExpectedUpdatedAt.Time.Equal(now) {
		t.Fatalf("updated params = %#v", p)
	}
}

func TestEquipmentEditUpdateAndRetirementMapPersistenceFailures(t *testing.T) {
	id, actor := uuid.New(), uuid.New()
	now := time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC)
	current := dbgen.Equipment{ID: id, AssetTag: "B-01", Name: "K1", Type: "Boat", Status: "Operational", UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}

	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"missing", pgx.ErrNoRows, http.StatusNotFound},
		{"service failure", errors.New("database unavailable"), http.StatusInternalServerError},
	} {
		t.Run("edit "+tc.name, func(t *testing.T) {
			h := Dashboard{Equipment: &equipmentStoreFake{getErr: tc.err}}
			r := equipmentRequest(http.MethodGet, "/admin/fleet/equipment/"+id.String(), nil, actor)
			r.SetPathValue("id", id.String())
			w := httptest.NewRecorder()
			h.EditEquipment(w, r)
			if w.Code != tc.want {
				t.Fatalf("response=%d want=%d", w.Code, tc.want)
			}
		})
		t.Run("update "+tc.name, func(t *testing.T) {
			h := Dashboard{Equipment: &equipmentStoreFake{getErr: tc.err}, Location: time.UTC}
			r := equipmentRequest(http.MethodPost, "/admin/fleet/equipment/"+id.String(), url.Values{"asset_tag": {"B-01"}, "name": {"K1"}, "type": {"Boat"}, "status": {"Operational"}, "expected_updated_at": {now.Format(time.RFC3339Nano)}}, actor)
			r.SetPathValue("id", id.String())
			w := httptest.NewRecorder()
			h.UpdateEquipment(w, r)
			if w.Code != tc.want {
				t.Fatalf("response=%d want=%d", w.Code, tc.want)
			}
		})
		t.Run("retirement preview "+tc.name, func(t *testing.T) {
			h := Dashboard{Equipment: &equipmentStoreFake{getErr: tc.err}}
			r := equipmentRequest(http.MethodGet, "/admin/fleet/equipment/"+id.String()+"/retire", nil, actor)
			r.SetPathValue("id", id.String())
			w := httptest.NewRecorder()
			h.RetireEquipmentPage(w, r)
			if w.Code != tc.want {
				t.Fatalf("response=%d want=%d", w.Code, tc.want)
			}
		})
	}

	t.Run("update duplicate and service failures retain user input", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			err  error
			want int
		}{
			{"duplicate asset tag", &pgconn.PgError{Code: "23505"}, http.StatusUnprocessableEntity},
			{"write failure", errors.New("database unavailable"), http.StatusInternalServerError},
		} {
			t.Run(tc.name, func(t *testing.T) {
				store := &equipmentStoreFake{equipment: current, updateErr: tc.err}
				h := Dashboard{Equipment: store, Location: time.UTC}
				r := equipmentRequest(http.MethodPost, "/admin/fleet/equipment/"+id.String(), url.Values{"asset_tag": {"B-02"}, "name": {"K1 revisto"}, "type": {"Boat"}, "status": {"Maintenance"}, "expected_updated_at": {now.Format(time.RFC3339Nano)}}, actor)
				r.SetPathValue("id", id.String())
				w := httptest.NewRecorder()
				h.UpdateEquipment(w, r)
				if w.Code != tc.want {
					t.Fatalf("response=%d want=%d", w.Code, tc.want)
				}
			})
		}
	})

	t.Run("retired equipment cannot be previewed again", func(t *testing.T) {
		h := Dashboard{Equipment: &equipmentStoreFake{equipment: dbgen.Equipment{ID: id, Status: "Retired"}}}
		r := equipmentRequest(http.MethodGet, "/admin/fleet/equipment/"+id.String()+"/retire", nil, actor)
		r.SetPathValue("id", id.String())
		w := httptest.NewRecorder()
		h.RetireEquipmentPage(w, r)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "já foi retirado") {
			t.Fatalf("response=%d body=%s", w.Code, w.Body.String())
		}
	})
}

func TestEquipmentLifecycleMapsMissingAndServiceFailures(t *testing.T) {
	id, actor := uuid.New(), uuid.New()
	for _, tc := range []struct {
		name  string
		store *equipmentStoreFake
		want  int
	}{
		{"missing equipment", &equipmentStoreFake{getErr: pgx.ErrNoRows}, http.StatusNotFound},
		{"read failure", &equipmentStoreFake{getErr: errors.New("database unavailable")}, http.StatusInternalServerError},
		{"reactivation conflict", &equipmentStoreFake{equipment: dbgen.Equipment{ID: id}, reactivateErr: pgx.ErrNoRows}, http.StatusConflict},
		{"reactivation failure", &equipmentStoreFake{equipment: dbgen.Equipment{ID: id}, reactivateErr: errors.New("database unavailable")}, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := Dashboard{Equipment: tc.store}
			r := equipmentRequest(http.MethodPost, "/admin/fleet/equipment/"+id.String()+"/reactivate", nil, actor)
			r.SetPathValue("id", id.String())
			w := httptest.NewRecorder()
			h.ReactivateEquipment(w, r)
			if w.Code != tc.want {
				t.Fatalf("response=%d want=%d", w.Code, tc.want)
			}
		})
	}
}

func TestEquipmentLifecycleUsesAuditedMutations(t *testing.T) {
	id, actor := uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for _, tc := range []struct {
		name, path string
		retire     bool
	}{
		{name: "retire", path: "/admin/fleet/equipment/" + id.String() + "/retire", retire: true},
		{name: "reactivate", path: "/admin/fleet/equipment/" + id.String() + "/reactivate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &equipmentStoreFake{equipment: dbgen.Equipment{ID: id, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}}
			h := Dashboard{Equipment: store}
			values := url.Values{}
			if tc.retire {
				values.Set("confirm_retirement", "yes")
				values.Set("expected_updated_at", now.Format(time.RFC3339Nano))
			}
			r := equipmentRequest(http.MethodPost, tc.path, values, actor)
			r.SetPathValue("id", id.String())
			w := httptest.NewRecorder()
			if tc.retire {
				h.RetireEquipment(w, r)
			} else {
				h.ReactivateEquipment(w, r)
			}
			if w.Code != http.StatusSeeOther {
				t.Fatalf("status = %d", w.Code)
			}
			if tc.retire && (store.retireParams.EquipmentID != id || store.retireParams.ActorUserID != actor || !store.retireParams.ExpectedUpdatedAt.Valid || !store.retireParams.ExpectedUpdatedAt.Time.Equal(now)) {
				t.Fatalf("retire = %#v", store.retireParams)
			}
			if !tc.retire && (store.reactivateParams.EquipmentID != id || store.reactivateParams.ActorUserID != actor) {
				t.Fatalf("reactivate = %#v", store.reactivateParams)
			}
		})
	}
}

func TestEquipmentLifecycleConflict(t *testing.T) {
	id := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	store := &equipmentStoreFake{equipment: dbgen.Equipment{ID: id, AssetTag: "K-01", Name: "Nome atualizado", UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, retireErr: pgx.ErrNoRows}
	h := Dashboard{Equipment: store}
	r := equipmentRequest(http.MethodPost, "/admin/fleet/equipment/"+id.String()+"/retire", url.Values{"confirm_retirement": {"yes"}, "expected_updated_at": {now.Add(-time.Minute).Format(time.RFC3339Nano)}}, uuid.New())
	r.SetPathValue("id", id.String())
	w := httptest.NewRecorder()
	h.RetireEquipment(w, r)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "alterado entretanto") || !strings.Contains(w.Body.String(), now.Format(time.RFC3339Nano)) {
		t.Fatalf("response = %d %s", w.Code, w.Body.String())
	}
}

func TestEquipmentRetirementPreviewAndConfirmation(t *testing.T) {
	id := uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond)
	store := &equipmentStoreFake{equipment: dbgen.Equipment{ID: id, AssetTag: "K-01", Name: "Kayak competição", Type: "Boat", Status: "Operational", UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}}
	h := Dashboard{Equipment: store}

	request := equipmentRequest(http.MethodGet, "/admin/fleet/equipment/"+id.String()+"/retire?return_to=%2Fadmin%2Ffleet%3Fequipment_page%3D2%23equipment-"+id.String(), nil, uuid.New())
	request.SetPathValue("id", id.String())
	response := httptest.NewRecorder()
	h.RetireEquipmentPage(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Retirar equipamento da frota") || !strings.Contains(response.Body.String(), "manutenções ativas") || !strings.Contains(response.Body.String(), `name="confirm_retirement"`) || !strings.Contains(response.Body.String(), `name="expected_updated_at" value="`+now.Format(time.RFC3339Nano)+`"`) || !strings.Contains(response.Body.String(), "equipment_page%3D2") {
		t.Fatalf("preview = %d %s", response.Code, response.Body.String())
	}

	request = equipmentRequest(http.MethodPost, "/admin/fleet/equipment/"+id.String()+"/retire", url.Values{"expected_updated_at": {now.Format(time.RFC3339Nano)}}, uuid.New())
	request.SetPathValue("id", id.String())
	response = httptest.NewRecorder()
	h.RetireEquipment(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "Confirme que pretende") {
		t.Fatalf("confirmation = %d %s", response.Code, response.Body.String())
	}

	request = equipmentRequest(http.MethodPost, "/admin/fleet/equipment/"+id.String()+"/retire", url.Values{"confirm_retirement": {"yes"}, "expected_updated_at": {"not-a-timestamp"}}, uuid.New())
	request.SetPathValue("id", id.String())
	response = httptest.NewRecorder()
	h.RetireEquipment(response, request)
	if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "confirmação expirou") || !strings.Contains(response.Body.String(), now.Format(time.RFC3339Nano)) || store.retireParams.EquipmentID != uuid.Nil {
		t.Fatalf("version = %d params=%#v %s", response.Code, store.retireParams, response.Body.String())
	}
}

func TestEquipmentAuditRenderingUsesReadableMetadataWithoutLosingChangeDetail(t *testing.T) {
	beforeKey, afterKey := "equipment/before.png", "equipment/after.png"
	before := []byte(`{"asset_tag":"B-01","name":"K1","type":"Boat","status":"Operational","notes":"Azul","image_object_key":"equipment/before.png"}`)
	after := []byte(`{"asset_tag":"B-02","name":"K2","type":"Paddle","status":"Maintenance","notes":"Revisto","image_object_key":"equipment/after.png"}`)
	rows := []dbgen.ListEquipmentAuditEventsRow{{Action: "UPDATED", ActorName: "Admin", OccurredAt: pgtype.Timestamptz{Time: time.Date(2026, 9, 8, 10, 0, 0, 0, time.UTC), Valid: true}, BeforeState: before, AfterState: after, AffectedMaintenanceIds: []uuid.UUID{uuid.New()}}}
	items := equipmentAuditItems(rows, time.UTC)
	if len(items) != 1 || items[0].Action != "Equipamento atualizado" || items[0].CancelledMaintenance != 1 || !strings.Contains(strings.Join(items[0].Changes, "|"), "Identificador: B-01 → B-02") || !strings.Contains(strings.Join(items[0].Changes, "|"), "Fotografia atualizada") {
		t.Fatalf("items = %#v", items)
	}
	if !sameOptionalString(&beforeKey, &beforeKey) || sameOptionalString(&beforeKey, &afterKey) || equipmentTypeName("Vehicle") != "Veículo" || equipmentStatusName("Retired") != "Retirado" || equipmentAuditAction("UNKNOWN") != "UNKNOWN" {
		t.Fatal("equipment audit display helpers returned an unexpected value")
	}
}

func TestEquipmentObjectCleanupAndAuditActionLabels(t *testing.T) {
	objects := &repairObjectStoreFake{}
	request := httptest.NewRequest(http.MethodPost, "/admin/fleet/equipment", nil)
	key := "equipment/2026/09/boat.png"
	Dashboard{}.deleteEquipmentObject(request, &key)
	(Dashboard{Objects: objects}).deleteEquipmentObject(request, nil)
	(Dashboard{Objects: objects}).deleteEquipmentObject(request, &key)
	if objects.deletes != 1 {
		t.Fatalf("deletes=%d", objects.deletes)
	}
	for action, want := range map[string]string{
		"CREATED": "Equipamento criado", "UPDATED": "Equipamento atualizado", "RETIRED": "Equipamento retirado", "REACTIVATED": "Equipamento reativado",
	} {
		if got := equipmentAuditAction(action); got != want {
			t.Errorf("equipmentAuditAction(%q)=%q, want %q", action, got, want)
		}
	}
}

func equipmentRequest(method, path string, values url.Values, userID uuid.UUID) *http.Request {
	body := ""
	if values != nil {
		body = values.Encode()
	}
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r.WithContext(context.WithValue(r.Context(), currentUserKey{}, CurrentUser{ID: userID, Name: "Administradora", IsAdmin: true}))
}
