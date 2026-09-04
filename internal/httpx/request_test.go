package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequestHelpersRecognizeHTMXAndUseSafeRedirects(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/source", nil)
	if IsHTMX(request) {
		t.Fatal("ordinary request identified as HTMX")
	}
	request.Header.Set("HX-Request", "true")
	if !IsHTMX(request) {
		t.Fatal("HTMX request was not identified")
	}
	w := httptest.NewRecorder()
	Redirect(w, request, "/target", http.StatusSeeOther)
	if w.Code != http.StatusOK || w.Header().Get("HX-Redirect") != "/target" {
		t.Fatalf("response=%d headers=%v", w.Code, w.Header())
	}
}
