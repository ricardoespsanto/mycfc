package app

import (
	"io/fs"
	"net/http"
	"regexp"
	"strings"

	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/featureflags"
	"github.com/cfcoimbra/mycfc/internal/handlers"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	staticassets "github.com/cfcoimbra/mycfc/ui/static"
)

var fingerprintedAsset = regexp.MustCompile(`-[0-9a-f]{12}\.(?:css|js|png)$`)

func newRouter(pool handlers.DBPinger, sessions *scs.SessionManager, landing handlers.Landing, login handlers.Login, registration handlers.Registration, emailVerification handlers.EmailVerification, passwordRecovery handlers.PasswordRecovery, auth handlers.Auth, dashboard handlers.Dashboard, repair handlers.Repair, events handlers.Events, announcements handlers.Announcements, training handlers.Training, structuredTraining handlers.StructuredTraining, members handlers.Members, profile handlers.Profile, news handlers.News, suggestions handlers.Suggestions, photoAlbums handlers.PhotoAlbums, foundation handlers.Foundation) http.Handler {
	mux := http.NewServeMux()
	health := handlers.Health{DB: pool}
	system := handlers.System(foundation)

	mux.HandleFunc("GET /health/live", health.Live)
	mux.HandleFunc("GET /health/ready", health.Ready)
	mux.Handle("GET /assets/{path...}", assetHandler())

	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if httpx.UserID(r.Context()) != "" {
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
			return
		}
		landing.Get(w, r)
	})

	mux.Handle("GET /login", auth.AnonymousOnly(http.HandlerFunc(login.Get)))
	mux.Handle("POST /login", auth.AnonymousOnly(http.HandlerFunc(login.Post)))
	mux.Handle("GET /registo", auth.AnonymousOnly(http.HandlerFunc(registration.Get)))
	mux.Handle("POST /registo", auth.AnonymousOnly(http.HandlerFunc(registration.Post)))
	mux.Handle("GET /recuperar-palavra-passe", auth.AnonymousOnly(http.HandlerFunc(passwordRecovery.RequestGet)))
	mux.Handle("POST /recuperar-palavra-passe", auth.AnonymousOnly(http.HandlerFunc(passwordRecovery.RequestPost)))
	mux.Handle("GET /recuperar-palavra-passe/repor", auth.AnonymousOnly(http.HandlerFunc(passwordRecovery.ResetGet)))
	mux.Handle("POST /recuperar-palavra-passe/repor", auth.AnonymousOnly(http.HandlerFunc(passwordRecovery.ResetPost)))
	mux.HandleFunc("GET /verificar-email", emailVerification.Confirm)
	mux.Handle("POST /logout", auth.RequireAuthenticated(http.HandlerFunc(auth.Logout)))
	mux.Handle("GET /perfil", auth.RequireAuthenticated(http.HandlerFunc(profile.Get)))
	mux.Handle("POST /perfil", auth.RequireAuthenticated(http.HandlerFunc(profile.Post)))
	mux.Handle("POST /perfil/email-verificacao/reenviar", auth.RequireAuthenticated(http.HandlerFunc(emailVerification.Resend)))
	mux.Handle("POST /perfil/fotografia", auth.RequireAuthenticated(http.HandlerFunc(profile.UploadPhoto)))
	mux.Handle("POST /perfil/fotografia/remover", auth.RequireAuthenticated(http.HandlerFunc(profile.RemovePhoto)))
	mux.Handle("GET /perfil/dependentes/{id}", auth.RequireGuardian(http.HandlerFunc(profile.Get)))
	mux.Handle("POST /perfil/dependentes/{id}", auth.RequireGuardian(http.HandlerFunc(profile.Post)))
	mux.Handle("POST /perfil/dependentes/{id}/fotografia", auth.RequireGuardian(http.HandlerFunc(profile.UploadPhoto)))
	mux.Handle("POST /perfil/dependentes/{id}/fotografia/remover", auth.RequireGuardian(http.HandlerFunc(profile.RemovePhoto)))
	mux.Handle("GET /membros/{id}/foto", auth.RequireAuthenticated(http.HandlerFunc(profile.Avatar)))
	mux.Handle("GET /dashboard", auth.RequireAuthenticated(http.HandlerFunc(auth.Dashboard)))
	mux.Handle("GET /today", auth.RequireAuthenticated(http.HandlerFunc(dashboard.Today)))
	mux.Handle("GET /dashboard/member", auth.RequireAuthenticated(compatibilityRedirect("/today")))
	mux.Handle("GET /dashboard/competitor", auth.RequireProgramme("Competition", "Initiation", "Kayak_Polo")(compatibilityRedirect("/today")))
	mux.Handle("GET /dashboard/initiation", auth.RequireProgramme("Initiation")(http.HandlerFunc(dashboard.Initiation)))
	mux.Handle("GET /dashboard/competition", auth.RequireProgramme("Competition")(http.HandlerFunc(dashboard.Competition)))
	mux.Handle("GET /dashboard/kayak-polo", auth.RequireProgramme("Kayak_Polo")(http.HandlerFunc(dashboard.KayakPolo)))
	mux.Handle("GET /dashboard/leisure", auth.RequireProgramme("Leisure")(http.HandlerFunc(dashboard.Leisure)))
	mux.Handle("GET /dashboard/guardian", auth.RequireGuardian(http.HandlerFunc(dashboard.Guardian)))
	mux.Handle("GET /dashboard/coach", auth.RequireCoach(compatibilityRedirect("/events")))
	mux.Handle("GET /dashboard/moderator", auth.RequireModerator(http.HandlerFunc(dashboard.Moderator)))
	mux.Handle("GET /admin/fleet", auth.RequireAdmin(http.HandlerFunc(dashboard.Admin)))
	mux.Handle("POST /admin/fleet/equipment", auth.RequireAdmin(http.HandlerFunc(dashboard.CreateEquipment)))
	mux.Handle("GET /admin/fleet/equipment/{id}/edit", auth.RequireAdmin(http.HandlerFunc(dashboard.EditEquipment)))
	mux.Handle("POST /admin/fleet/equipment/{id}", auth.RequireAdmin(http.HandlerFunc(dashboard.UpdateEquipment)))
	mux.Handle("POST /admin/fleet/equipment/{id}/retire", auth.RequireAdmin(http.HandlerFunc(dashboard.RetireEquipment)))
	mux.Handle("POST /admin/fleet/equipment/{id}/reactivate", auth.RequireAdmin(http.HandlerFunc(dashboard.ReactivateEquipment)))
	mux.Handle("GET /admin/sistema", auth.RequireAdmin(http.HandlerFunc(dashboard.ReleasesPage)))
	mux.Handle("POST /admin/sistema/funcionalidades/{key}", auth.RequireAdmin(http.HandlerFunc(dashboard.UpdateFeatureFlag)))
	mux.Handle("POST /admin/maintenance", auth.RequireAdmin(http.HandlerFunc(dashboard.Maintenance)))
	mux.Handle("POST /admin/repairs/status", auth.RequireAdmin(http.HandlerFunc(dashboard.RepairStatus)))
	mux.Handle("POST /admin/maintenance/{id}/complete", auth.RequireAdmin(http.HandlerFunc(dashboard.CompleteMaintenance)))
	mux.Handle("GET /admin/membros", auth.RequireAdmin(http.HandlerFunc(members.Index)))
	mux.Handle("POST /admin/membros", auth.RequireAdmin(http.HandlerFunc(members.Create)))
	mux.Handle("GET /admin/membros/{id}", auth.RequireAdmin(http.HandlerFunc(members.Detail)))
	mux.Handle("GET /admin/membros/{id}/perfil", auth.RequireAdmin(http.HandlerFunc(profile.Get)))
	mux.Handle("POST /admin/membros/{id}/perfil", auth.RequireAdmin(http.HandlerFunc(profile.Post)))
	mux.Handle("POST /admin/membros/{id}/perfil/fotografia", auth.RequireAdmin(http.HandlerFunc(profile.UploadPhoto)))
	mux.Handle("POST /admin/membros/{id}/perfil/fotografia/remover", auth.RequireAdmin(http.HandlerFunc(profile.RemovePhoto)))
	mux.Handle("POST /admin/membros/{id}/inscricao", auth.RequireAdmin(http.HandlerFunc(members.Membership)))
	mux.Handle("POST /admin/membros/{id}/desativar", auth.RequireAdmin(http.HandlerFunc(members.Deactivate)))
	mux.Handle("POST /admin/membros/{id}/credencial-menor", auth.RequireAdmin(http.HandlerFunc(members.IssueMinorCredential)))
	mux.Handle("GET /admin/noticias", auth.RequireAdmin(http.HandlerFunc(news.Index)))
	mux.Handle("POST /admin/noticias", auth.RequireAdmin(http.HandlerFunc(news.Create)))
	mux.Handle("POST /admin/noticias/{id}/publicar", auth.RequireAdmin(http.HandlerFunc(news.Publish)))
	mux.Handle("POST /admin/noticias/{id}/expirar", auth.RequireAdmin(http.HandlerFunc(news.Expire)))
	mux.Handle("POST /repairs", auth.RequireAuthenticated(http.HandlerFunc(repair.Post)))
	mux.Handle("GET /fleet", auth.RequireAuthenticated(http.HandlerFunc(repair.Index)))
	mux.Handle("POST /guardian/add-dependent", auth.RequireGuardian(http.HandlerFunc(dashboard.AddDependent)))
	mux.Handle("POST /dashboard/guardian/dependents/{id}/leaderboard-privacy", auth.RequireGuardian(http.HandlerFunc(dashboard.DependentLeaderboardPrivacy)))
	mux.Handle("POST /leaderboard/privacy", auth.RequireAuthenticated(http.HandlerFunc(dashboard.LeaderboardPrivacy)))
	mux.Handle("GET /events", auth.RequireAuthenticated(http.HandlerFunc(events.Index)))
	mux.Handle("GET /events/{id}", auth.RequireAuthenticated(http.HandlerFunc(events.Detail)))
	mux.Handle("POST /events/{id}/responses", auth.RequireAuthenticated(http.HandlerFunc(events.Respond)))
	mux.Handle("GET /admin/eventos", auth.RequireEventStaff(http.HandlerFunc(events.Index)))
	mux.Handle("GET /admin/eventos/{id}", auth.RequireEventStaff(http.HandlerFunc(events.Detail)))
	mux.Handle("GET /admin/eventos/{id}/editar", auth.RequireEventStaff(http.HandlerFunc(events.Edit)))
	mux.Handle("POST /admin/events", auth.RequireEventStaff(http.HandlerFunc(events.Create)))
	mux.Handle("POST /admin/events/{id}", auth.RequireEventStaff(http.HandlerFunc(events.Update)))
	mux.Handle("POST /admin/events/{id}/cancel", auth.RequireEventStaff(http.HandlerFunc(events.Cancel)))
	mux.Handle("POST /admin/events/{id}/confirm", auth.RequireEventStaff(http.HandlerFunc(events.Confirm)))
	mux.Handle("POST /admin/events/{id}/check-in", auth.RequireEventStaff(http.HandlerFunc(events.CheckIn)))
	mux.Handle("GET /announcements", auth.RequireAuthenticated(http.HandlerFunc(announcements.Index)))
	mux.Handle("GET /announcements/panel", auth.RequireAuthenticated(http.HandlerFunc(announcements.Panel)))
	mux.Handle("GET /announcements/{id}", auth.RequireAuthenticated(http.HandlerFunc(announcements.Detail)))
	mux.Handle("GET /admin/avisos", auth.RequireEventStaff(http.HandlerFunc(announcements.Index)))
	mux.Handle("POST /admin/announcements", auth.RequireEventStaff(http.HandlerFunc(announcements.Create)))
	mux.Handle("POST /admin/announcements/{id}/publish", auth.RequireEventStaff(http.HandlerFunc(announcements.Publish)))
	mux.Handle("POST /admin/announcements/{id}/expire", auth.RequireEventStaff(http.HandlerFunc(announcements.Expire)))
	mux.Handle("GET /treinos", auth.RequireAuthenticated(http.HandlerFunc(training.Index)))
	mux.Handle("GET /admin/treinos", auth.RequireEventStaff(http.HandlerFunc(training.Index)))
	mux.Handle("POST /admin/treinos/planos", auth.RequireEventStaff(http.HandlerFunc(training.CreatePlan)))
	mux.Handle("POST /admin/treinos/sessoes", auth.RequireEventStaff(http.HandlerFunc(training.CreateSession)))
	mux.Handle("GET /admin/treinos/sessoes/{id}/editar", auth.RequireEventStaff(http.HandlerFunc(training.EditSession)))
	mux.Handle("POST /admin/treinos/sessoes/{id}", auth.RequireEventStaff(http.HandlerFunc(training.UpdateSession)))
	mux.Handle("POST /admin/treinos/sessoes/{id}/cancelar", auth.RequireEventStaff(http.HandlerFunc(training.CancelSession)))
	mux.Handle("POST /treinos/sessoes/resultados", auth.RequireAuthenticated(http.HandlerFunc(training.ReportOutcome)))
	mux.Handle("POST /treinos/sessoes/distancia", auth.RequireAuthenticated(http.HandlerFunc(training.UpdateDistance)))
	mux.Handle("GET /treinos/estruturados", auth.RequireFeature(featureflags.StructuredTrainingPlanning, http.HandlerFunc(structuredTraining.Index)))
	mux.Handle("GET /admin/treinos/estruturados", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireEventStaff(http.HandlerFunc(structuredTraining.Index))))
	mux.Handle("POST /admin/treinos/estruturados/grupos", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireEventStaff(http.HandlerFunc(structuredTraining.CreateGroup))))
	mux.Handle("POST /admin/treinos/estruturados/semanas", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireEventStaff(http.HandlerFunc(structuredTraining.CreateWeek))))
	mux.Handle("POST /admin/treinos/estruturados/sessoes", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireEventStaff(http.HandlerFunc(structuredTraining.CreateSession))))
	mux.Handle("POST /admin/treinos/estruturados/sessoes/{id}/segmentos", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireEventStaff(http.HandlerFunc(structuredTraining.CreateSegment))))
	mux.Handle("POST /admin/treinos/estruturados/segmentos/{id}/blocos", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireEventStaff(http.HandlerFunc(structuredTraining.CreateBlock))))
	mux.Handle("POST /admin/treinos/estruturados/segmentos/{id}/ginasio", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireEventStaff(http.HandlerFunc(structuredTraining.CreateGymBlock))))
	mux.Handle("POST /admin/treinos/estruturados/blocos/{id}/exercicios", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireEventStaff(http.HandlerFunc(structuredTraining.CreateGymExercise))))
	mux.Handle("POST /admin/treinos/estruturados/segmentos/{id}/agua", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireEventStaff(http.HandlerFunc(structuredTraining.CreateWaterBlock))))
	mux.Handle("POST /admin/treinos/estruturados/blocos/{id}/agua/passos", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireEventStaff(http.HandlerFunc(structuredTraining.CreateWaterWorkStep))))
	mux.Handle("POST /admin/treinos/estruturados/agua/perfis", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireAdmin(http.HandlerFunc(structuredTraining.CreateWaterIntensityProfile))))
	mux.Handle("POST /admin/treinos/estruturados/agua/perfis/{id}/zonas", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireAdmin(http.HandlerFunc(structuredTraining.CreateWaterIntensityZone))))
	mux.Handle("POST /admin/treinos/estruturados/segmentos/{id}/mover", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireEventStaff(http.HandlerFunc(structuredTraining.MoveSegment))))
	mux.Handle("POST /admin/treinos/estruturados/blocos/{id}/mover", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireEventStaff(http.HandlerFunc(structuredTraining.MoveBlock))))
	mux.Handle("POST /admin/treinos/estruturados/exercicios/{id}/mover", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireEventStaff(http.HandlerFunc(structuredTraining.MoveGymExercise))))
	mux.Handle("POST /admin/treinos/estruturados/rotinas", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireEventStaff(http.HandlerFunc(structuredTraining.CreateRoutine))))
	mux.Handle("POST /admin/treinos/estruturados/rotinas/{id}/inserir", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireEventStaff(http.HandlerFunc(structuredTraining.InsertRoutine))))
	mux.Handle("POST /admin/treinos/estruturados/blocos/{id}/copiar", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireEventStaff(http.HandlerFunc(structuredTraining.CopyBlock))))
	mux.Handle("POST /admin/treinos/estruturados/sessoes/{id}/copiar", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireEventStaff(http.HandlerFunc(structuredTraining.CopySession))))
	mux.Handle("POST /admin/treinos/estruturados/dias/copiar", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireEventStaff(http.HandlerFunc(structuredTraining.CopyDay))))
	mux.Handle("POST /admin/treinos/estruturados/semanas/{id}/copiar", auth.RequireFeature(featureflags.StructuredTrainingPlanning, auth.RequireEventStaff(http.HandlerFunc(structuredTraining.CopyWeek))))
	mux.Handle("GET /sugestoes", auth.RequireFeature(featureflags.Suggestions, http.HandlerFunc(suggestions.Index)))
	mux.Handle("POST /sugestoes", auth.RequireFeature(featureflags.Suggestions, http.HandlerFunc(suggestions.Create)))
	mux.Handle("GET /admin/sugestoes", auth.RequireFeature(featureflags.Suggestions, auth.RequireSuggestionStaff(http.HandlerFunc(suggestions.Index))))
	mux.Handle("POST /admin/sugestoes/{id}", auth.RequireFeature(featureflags.Suggestions, auth.RequireSuggestionStaff(http.HandlerFunc(suggestions.Update))))
	mux.Handle("GET /albuns", auth.RequireAuthenticated(http.HandlerFunc(photoAlbums.Index)))
	mux.Handle("GET /albuns/{id}", auth.RequireAuthenticated(http.HandlerFunc(photoAlbums.Detail)))
	mux.Handle("GET /admin/albuns", auth.RequireContentStaff(http.HandlerFunc(photoAlbums.Index)))
	mux.Handle("GET /admin/albuns/{id}", auth.RequireContentStaff(http.HandlerFunc(photoAlbums.Detail)))
	mux.Handle("POST /admin/albuns", auth.RequireContentStaff(http.HandlerFunc(photoAlbums.Create)))
	mux.Handle("POST /admin/albuns/{id}/arquivar", auth.RequireContentStaff(http.HandlerFunc(photoAlbums.Archive)))

	return customNotFound(mux, system.NotFound)
}

// compatibilityRedirect keeps an approved legacy URL working without weakening
// the capability middleware wrapped around it. Query parameters are retained so
// bookmarked filters and return context are not silently discarded.
func compatibilityRedirect(target string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		location := target
		if r.URL.RawQuery != "" {
			location += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, location, http.StatusSeeOther)
	})
}

func assetHandler() http.Handler {
	dist, err := fs.Sub(staticassets.Files, "dist")
	if err != nil {
		panic(err)
	}
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assetPath := strings.TrimPrefix(r.URL.Path, "/assets/")
		if fingerprintedAsset.MatchString(assetPath) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		clone := r.Clone(r.Context())
		clonedURL := *r.URL
		clonedURL.Path = "/" + assetPath
		clone.URL = &clonedURL
		files.ServeHTTP(w, clone)
	})
}

func customNotFound(mux *http.ServeMux, notFound http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, pattern := mux.Handler(r)
		if pattern != "" || matchesOtherMethod(mux, r) {
			// ServeMux attaches path values such as {id} before invoking a handler.
			mux.ServeHTTP(w, r)
			return
		}
		notFound(w, r)
	})
}

func matchesOtherMethod(mux *http.ServeMux, request *http.Request) bool {
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodHead} {
		if method == request.Method {
			continue
		}
		clone := request.Clone(request.Context())
		clone.Method = method
		_, pattern := mux.Handler(clone)
		if pattern != "" {
			return true
		}
	}
	return false
}
