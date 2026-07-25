package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/alexedwards/scs/v2"
	"github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/cfcoimbra/mycfc/internal/httpx"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type CurrentUserLookup interface {
	GetActiveAccountByID(context.Context, uuid.UUID) (dbgen.GetActiveAccountByIDRow, error)
	ListActiveMembershipProgrammeCodesForUser(context.Context, uuid.UUID) ([]string, error)
}

type CurrentUser struct {
	ID         uuid.UUID
	Name       string
	IsAdmin    bool
	Programmes map[string]bool
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
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (!user.IsActive || user.IsDependent)) {
			a.destroy(r.Context())
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			a.System.InternalError(w, r)
			return
		}
		programmes, err := a.Users.ListActiveMembershipProgrammeCodesForUser(r.Context(), id)
		if err != nil {
			a.System.InternalError(w, r)
			return
		}
		current := CurrentUser{ID: user.ID, Name: user.Name, IsAdmin: user.IsAdmin, Programmes: make(map[string]bool, len(programmes))}
		for _, programme := range programmes {
			current.Programmes[programme] = true
		}
		ctx := context.WithValue(r.Context(), currentUserKey{}, current)
		ctx = httpx.WithUserID(ctx, current.ID.String())
		next.ServeHTTP(w, r.WithContext(ctx))
	})
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
	user, _ := currentUser(r.Context())
	if user.Programmes["Leisure"] {
		http.Redirect(w, r, "/dashboard/leisure", http.StatusSeeOther)
		return
	}
	for _, programme := range []string{"Competition", "Initiation", "Kayak_Polo"} {
		if user.Programmes[programme] {
			http.Redirect(w, r, "/dashboard/competitor", http.StatusSeeOther)
			return
		}
	}
	if user.IsAdmin {
		http.Redirect(w, r, "/admin/fleet", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/dashboard/member", http.StatusSeeOther)
}

func (a Auth) destroy(ctx context.Context) {
	_ = a.Sessions.Destroy(ctx)
}

func currentUser(ctx context.Context) (CurrentUser, bool) {
	user, ok := ctx.Value(currentUserKey{}).(CurrentUser)
	return user, ok
}

func CurrentUserFromContext(ctx context.Context) (CurrentUser, bool) { return currentUser(ctx) }
