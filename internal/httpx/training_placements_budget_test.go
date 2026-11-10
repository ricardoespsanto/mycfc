package httpx

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTrainingPlacementIngressBoundsEveryNativeWriteBeforeParsing(t *testing.T) {
	for _, suffix := range []string{"/colocacoes", "/colocacoes/prever", "/colocacoes/confirmar", "/colocacoes/cancelar/prever", "/colocacoes/cancelar/confirmar", "/grupos/39100000-0000-0000-0000-000000000099/atletas"} {
		for _, extra := range []int{0, 1} {
			h := PublicationIngressMiddleware(30 * time.Second)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, e := io.ReadAll(r.Body)
				if (e != nil) != (extra == 1) {
					t.Fatalf("%s limit delta%d err%v", suffix, extra, e)
				}
				if _, ok := PublicationDeadline(r.Context(), 30*time.Second); !ok {
					t.Fatal("missing original business clock")
				}
			}))
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/admin/treinos/estruturados"+suffix, strings.NewReader(strings.Repeat("x", int(TrainingPlacementWriteMaxBytes)+extra))))
		}
	}
}
