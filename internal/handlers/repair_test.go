package handlers

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestRepairPostNoPhotoCreatesAndRedirects(t *testing.T) {
	userID, equipmentID := uuid.New(), uuid.New()
	store := &repairStoreFake{equipment: dbgen.Equipment{ID: equipmentID, Status: "Operational"}}
	response := repairResponse(t, Repair{Store: store}, userID, equipmentID, nil, false)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/dashboard" {
		t.Fatalf("response = %d %q", response.Code, response.Header().Get("Location"))
	}
	if store.created.ImageObjectKey != nil || store.created.IssueDescription != "Uma descrição válida" || store.creates != 1 {
		t.Fatalf("created = %#v, creates = %d", store.created, store.creates)
	}
}

func TestRepairPostPhotoStoresValidatedMetadata(t *testing.T) {
	userID, equipmentID := uuid.New(), uuid.New()
	store := &repairStoreFake{equipment: dbgen.Equipment{ID: equipmentID, Status: "Operational"}}
	objects := &repairObjectStoreFake{}
	response := repairResponse(t, Repair{Store: store, Objects: objects}, userID, equipmentID, pngPhoto(t), false)
	if response.Code != http.StatusSeeOther || objects.puts != 1 {
		t.Fatalf("status = %d, puts = %d", response.Code, objects.puts)
	}
	if store.created.ImageContentType == nil || *store.created.ImageContentType != "image/png" || store.created.ImageSizeBytes == nil || store.created.ImageObjectKey == nil || !strings.HasPrefix(*store.created.ImageObjectKey, "repairs/") || !strings.HasSuffix(*store.created.ImageObjectKey, ".png") {
		t.Fatalf("created metadata = %#v", store.created)
	}
}

func TestRepairPostRejectsInvalidPhotoBeforeStorage(t *testing.T) {
	userID, equipmentID := uuid.New(), uuid.New()
	store := &repairStoreFake{equipment: dbgen.Equipment{ID: equipmentID, Status: "Operational"}}
	objects := &repairObjectStoreFake{}
	response := repairResponse(t, Repair{Store: store, Objects: objects}, userID, equipmentID, []byte("<svg></svg>"), false)
	if response.Code != http.StatusUnprocessableEntity || objects.puts != 0 || store.creates != 0 {
		t.Fatalf("status = %d, puts = %d, creates = %d", response.Code, objects.puts, store.creates)
	}
}

func TestRepairPostOversizedBodyReturns413(t *testing.T) {
	userID, equipmentID := uuid.New(), uuid.New()
	store := &repairStoreFake{equipment: dbgen.Equipment{ID: equipmentID, Status: "Operational"}}
	response := repairResponse(t, Repair{Store: store, MaxRequestBytes: 100}, userID, equipmentID, nil, false)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestRepairPostDatabaseFailureDeletesUploadedPhoto(t *testing.T) {
	userID, equipmentID := uuid.New(), uuid.New()
	store := &repairStoreFake{equipment: dbgen.Equipment{ID: equipmentID, Status: "Operational"}, createErr: errors.New("database down")}
	objects := &repairObjectStoreFake{}
	response := repairResponse(t, Repair{Store: store, Objects: objects}, userID, equipmentID, pngPhoto(t), false)
	if response.Code != http.StatusInternalServerError || objects.deletes != 1 {
		t.Fatalf("status = %d, deletes = %d", response.Code, objects.deletes)
	}
}

func TestRepairPostCrossUserIdempotencyConflict(t *testing.T) {
	userID, equipmentID := uuid.New(), uuid.New()
	otherID := uuid.New()
	store := &repairStoreFake{existing: &dbgen.RepairRequest{ID: uuid.New(), ReportedByID: &otherID}}
	response := repairResponse(t, Repair{Store: store}, userID, equipmentID, nil, false)
	if response.Code != http.StatusConflict || store.creates != 0 {
		t.Fatalf("status = %d, creates = %d", response.Code, store.creates)
	}
}

func TestRepairPostRetiredEquipmentIsRejected(t *testing.T) {
	userID, equipmentID := uuid.New(), uuid.New()
	store := &repairStoreFake{equipment: dbgen.Equipment{ID: equipmentID, Status: "Retired"}}
	response := repairResponse(t, Repair{Store: store}, userID, equipmentID, nil, false)
	if response.Code != http.StatusUnprocessableEntity || store.creates != 0 {
		t.Fatalf("status = %d, creates = %d", response.Code, store.creates)
	}
}

func TestRepairPostHTMXSuccessReplacesForm(t *testing.T) {
	userID, equipmentID := uuid.New(), uuid.New()
	store := &repairStoreFake{equipment: dbgen.Equipment{ID: equipmentID, Status: "Operational"}}
	response := repairResponse(t, Repair{Store: store}, userID, equipmentID, nil, true)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Avaria reportada") || !strings.Contains(response.Body.String(), "idempotency_key") {
		t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
	}
}

func repairResponse(t *testing.T, handler Repair, userID, equipmentID uuid.UUID, photo []byte, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("idempotency_key", uuid.NewString())
	_ = writer.WriteField("equipment_id", equipmentID.String())
	_ = writer.WriteField("issue_description", "Uma descrição válida")
	if photo != nil {
		part, err := writer.CreateFormFile("photo", "upload.png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(photo); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/repairs", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if htmx {
		request.Header.Set("HX-Request", "true")
	}
	request = request.WithContext(context.WithValue(request.Context(), currentUserKey{}, CurrentUser{ID: userID, Role: "Competitor"}))
	response := httptest.NewRecorder()
	handler.Post(response, request)
	return response
}

func pngPhoto(t *testing.T) []byte {
	t.Helper()
	var body bytes.Buffer
	if err := png.Encode(&body, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}

type repairStoreFake struct {
	existing  *dbgen.RepairRequest
	equipment dbgen.Equipment
	createErr error
	created   dbgen.CreateRepairRequestParams
	creates   int
}

func (s *repairStoreFake) GetRepairByIdempotencyKey(context.Context, uuid.UUID) (dbgen.RepairRequest, error) {
	if s.existing != nil {
		return *s.existing, nil
	}
	return dbgen.RepairRequest{}, pgx.ErrNoRows
}
func (s *repairStoreFake) GetEquipmentByID(context.Context, uuid.UUID) (dbgen.Equipment, error) {
	return s.equipment, nil
}
func (s *repairStoreFake) CreateRepairRequest(_ context.Context, input dbgen.CreateRepairRequestParams) (dbgen.RepairRequest, error) {
	s.creates++
	s.created = input
	if s.createErr != nil {
		return dbgen.RepairRequest{}, s.createErr
	}
	return dbgen.RepairRequest{ID: uuid.New(), ReportedByID: input.ReportedByID}, nil
}
func (s *repairStoreFake) ListOperationalEquipment(context.Context, int32) ([]dbgen.Equipment, error) {
	return []dbgen.Equipment{s.equipment}, nil
}

type repairObjectStoreFake struct{ puts, deletes int }

func (s *repairObjectStoreFake) PutRepairPhoto(_ context.Context, _ string, _ string, _ int64, body io.Reader) error {
	s.puts++
	_, _ = io.Copy(io.Discard, body)
	return nil
}
func (s *repairObjectStoreFake) DeleteObject(context.Context, string) error { s.deletes++; return nil }
func (s *repairObjectStoreFake) PresignGet(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
