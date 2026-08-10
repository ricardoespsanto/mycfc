package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type CurrentUserLookup interface {
	GetActiveAccountByID(context.Context, uuid.UUID) (dbgen.GetActiveAccountByIDRow, error)
	GetActiveAccountByIDWithoutProfile(context.Context, uuid.UUID) (dbgen.GetActiveAccountByIDWithoutProfileRow, error)
	ListActiveMembershipProgrammeCodesForUser(context.Context, uuid.UUID) ([]string, error)
	ListActiveStaffGrantsForUser(context.Context, uuid.UUID) ([]dbgen.ListActiveStaffGrantsForUserRow, error)
}

type CurrentUser struct {
	ID                 uuid.UUID
	Name               string
	Email              string
	EmailVerified      bool
	IsDependent        bool
	IsAdmin            bool
	LeaderboardVisible bool
	ProfileComplete    bool
	Programmes         map[string]bool
	CoachProgrammeIDs  map[uuid.UUID]bool
	CoachTeamIDs       map[uuid.UUID]bool
	CanManageEvents    bool
	CanModerateContent bool
}

type Auth struct {
	Users    CurrentUserLookup
	Sessions *scs.SessionManager
	System   System
}

type currentUserKey struct{}

func (a Auth) Load(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userID := a.Sessions.GetString(r.Context(), "user_id")
		if userID == "" {
			next.ServeHTTP(w, r)
			return
		}
		id, err := uuid.Parse(userID)
		if err != nil {
			a.destroy(r.Context())
			next.ServeHTTP(w, r)
			return
		}
		user, err := a.Users.GetActiveAccountByID(r.Context(), id)
		if profileSchemaUnavailable(err) {
			fallback, fallbackErr := a.Users.GetActiveAccountByIDWithoutProfile(r.Context(), id)
			if fallbackErr == nil {
				slog.Warn("profile schema unavailable; loading account without profile status", "request_id", httpx.RequestID(r.Context()))
				user = dbgen.GetActiveAccountByIDRow{
					ID: fallback.ID, Name: fallback.Name, Email: fallback.Email, IsDependent: fallback.IsDependent,
					IsActive: fallback.IsActive, LeaderboardVisible: fallback.LeaderboardVisible,
					EmailVerified: fallback.EmailVerified, IsAdmin: fallback.IsAdmin, ProfileComplete: true,
					CredentialVersion: fallback.CredentialVersion,
				}
			}
			err = fallbackErr
		}
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !user.IsActive) {
			a.destroy(r.Context())
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			a.System.InternalError(w, r)
			return
		}
		if sessionVersion := a.Sessions.GetInt64(r.Context(), "credential_version"); sessionVersion <= 0 || sessionVersion != user.CredentialVersion {
			a.destroy(r.Context())
			next.ServeHTTP(w, r)
			return
		}
		programmes, err := a.Users.ListActiveMembershipProgrammeCodesForUser(r.Context(), id)
		if err != nil {
			a.System.InternalError(w, r)
			return
		}
		current := CurrentUser{ID: user.ID, Name: user.Name, Email: stringValue(user.Email), EmailVerified: user.EmailVerified, IsDependent: user.IsDependent, IsAdmin: user.IsAdmin && !user.IsDependent, LeaderboardVisible: user.LeaderboardVisible, ProfileComplete: user.ProfileComplete, Programmes: make(map[string]bool, len(programmes)), CoachProgrammeIDs: map[uuid.UUID]bool{}, CoachTeamIDs: map[uuid.UUID]bool{}}
		for _, programme := range programmes {
			current.Programmes[programme] = true
		}
		var grants []dbgen.ListActiveStaffGrantsForUserRow
		if !user.IsDependent {
			grants, err = a.Users.ListActiveStaffGrantsForUser(r.Context(), id)
			if err != nil {
				a.System.InternalError(w, r)
				return
			}
		}
		for _, grant := range grants {
			switch grant.Capability {
			case "COACH":
				current.CanManageEvents = true
				if grant.ProgrammeID != nil {
					current.CoachProgrammeIDs[*grant.ProgrammeID] = true
				}
				if grant.TeamID != nil {
					current.CoachTeamIDs[*grant.TeamID] = true
				}
			case "MODERATOR":
				current.CanModerateContent = true
			}
		}
		ctx := context.WithValue(r.Context(), currentUserKey{}, current)
		ctx = httpx.WithUserID(ctx, current.ID.String())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func profileSchemaUnavailable(err error) bool {
	var postgresErr *pgconn.PgError
	return errors.As(err, &postgresErr) && (postgresErr.Code == "42P01" || postgresErr.Code == "42501")
}

func (a Auth) RequireGuardian(next http.Handler) http.Handler {
	return a.RequireAuthenticated(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _ := currentUser(r.Context())
		if user.IsDependent {
			a.System.Forbidden(w, r)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (a Auth) AnonymousOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := currentUser(r.Context()); ok {
			http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a Auth) RequireAuthenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := currentUser(r.Context()); !ok {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a Auth) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := currentUser(r.Context())
		if !ok {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
			return
		}
		if user.IsAdmin {
			next.ServeHTTP(w, r)
			return
		}
		a.System.Forbidden(w, r)
	})
}

func (a Auth) RequireEventStaff(next http.Handler) http.Handler {
	return a.RequireAuthenticated(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _ := currentUser(r.Context())
		if user.IsAdmin || user.CanManageEvents {
			next.ServeHTTP(w, r)
			return
		}
		a.System.Forbidden(w, r)
	}))
}

func (a Auth) RequireCoach(next http.Handler) http.Handler {
	return a.RequireAuthenticated(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _ := currentUser(r.Context())
		if user.CanManageEvents {
			next.ServeHTTP(w, r)
			return
		}
		a.System.Forbidden(w, r)
	}))
}

func (a Auth) RequireModerator(next http.Handler) http.Handler {
	return a.RequireAuthenticated(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _ := currentUser(r.Context())
		if user.CanModerateContent {
			next.ServeHTTP(w, r)
			return
		}
		a.System.Forbidden(w, r)
	}))
}

func (a Auth) RequireProgramme(programmes ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return a.RequireAuthenticated(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, _ := currentUser(r.Context())
			for _, programme := range programmes {
				if user.Programmes[programme] {
					next.ServeHTTP(w, r)
					return
				}
			}
			a.System.Forbidden(w, r)
		}))
	}
}

func (a Auth) Logout(w http.ResponseWriter, r *http.Request) {
	a.destroy(r.Context())
	httpx.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a Auth) Dashboard(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/today", http.StatusSeeOther)
}

func (a Auth) destroy(ctx context.Context) {
	_ = a.Sessions.Destroy(ctx)
}

func currentUser(ctx context.Context) (CurrentUser, bool) {
	user, ok := ctx.Value(currentUserKey{}).(CurrentUser)
	return user, ok
}

func CurrentUserFromContext(ctx context.Context) (CurrentUser, bool) { return currentUser(ctx) }
