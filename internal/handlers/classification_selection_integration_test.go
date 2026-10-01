//go:build integration

package handlers

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestClassificationUnavailableSubjectsAreNonDisclosing(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	admin, subject := uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{admin, subject} {
		if _, err = pool.Exec(ctx, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Unavailable search subject',$2,'hash','1980-01-01')`, id, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	defer pool.Exec(ctx, `DELETE FROM users WHERE id IN ($1,$2)`, admin, subject)
	if _, err = pool.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, admin); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DELETE FROM user_platform_roles WHERE user_id=$1`, admin)
	store := PostgresClassificationStore{Pool: pool}
	for _, unavailable := range []uuid.UUID{uuid.New(), subject} {
		if unavailable == subject {
			if _, err = pool.Exec(ctx, `UPDATE users SET is_active=false WHERE id=$1`, subject); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err = store.View(ctx, admin, unavailable); !errors.Is(err, ErrClassificationDenied) {
			t.Errorf("unavailable subject view: %v", err)
		}
		for _, method := range []string{"GET", "POST"} {
			r := httptest.NewRequest(method, "/equipa/classificacao/"+unavailable.String(), strings.NewReader("category_id=forged"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.SetPathValue("id", unavailable.String())
			r = r.WithContext(context.WithValue(ctx, currentUserKey{}, CurrentUser{ID: admin}))
			w := httptest.NewRecorder()
			h := Classification{Store: store}
			if method == "GET" {
				h.Get(w, r)
			} else {
				h.Post(w, r)
			}
			if w.Code != 404 || w.Body.String() != "404 page not found\n" {
				t.Errorf("%s unavailable: %d %s", method, w.Code, w.Body.String())
			}
		}
	}
}
