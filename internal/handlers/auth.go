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
	GetUserByID(context.Context, uuid.UUID) (dbgen.User, error)
}

type CurrentUser struct {
	ID   uuid.UUID
	Name string
	Role string
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
		user, err := a.Users.GetUserByID(r.Context(), id)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && (!user.IsActive || user.IsDependent)) {
			a.destroy(r.Context())
			next.ServeHTTP(w, r)
			return
		}
		if err != nil {
			a.System.InternalError(w, r)
			return
		}
		role, ok := user.Role.(string)
		if !ok {
			a.System.InternalError(w, r)
			return
		}
		current := CurrentUser{ID: user.ID, Name: user.Name, Role: role}
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

func (a Auth) RequireRole(roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := currentUser(r.Context())
			if !ok {
				http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
				return
			}
			for _, role := range roles {
				if user.Role == role {
					next.ServeHTTP(w, r)
					return
				}
			}
			a.System.Forbidden(w, r)
		})
	}
}

func (a Auth) Logout(w http.ResponseWriter, r *http.Request) {
	a.destroy(r.Context())
	httpx.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (a Auth) Dashboard(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r.Context())
	paths := map[string]string{"Admin": "/admin/fleet", "Competitor": "/dashboard/competitor", "Leisure": "/dashboard/leisure", "Guardian": "/dashboard/guardian"}
	http.Redirect(w, r, paths[user.Role], http.StatusSeeOther)
}

func (a Auth) destroy(ctx context.Context) {
	_ = a.Sessions.Destroy(ctx)
}

func currentUser(ctx context.Context) (CurrentUser, bool) {
	user, ok := ctx.Value(currentUserKey{}).(CurrentUser)
	return user, ok
}

func CurrentUserFromContext(ctx context.Context) (CurrentUser, bool) { return currentUser(ctx) }
