//go:build integration

package db

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestGuardianAgeHandoffPreservesIdentityAndFailsClosedAtMajority(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	lisbon, err := time.LoadLocation("Europe/Lisbon")
	if err != nil {
		t.Fatal(err)
	}
	today := time.Now().In(lisbon)
	guardianID, subjectID, adminID := uuid.New(), uuid.New(), uuid.New()
	for id, label := range map[uuid.UUID]string{guardianID: "Responsável handoff", adminID: "Administrador handoff"} {
		if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth) VALUES($1,$2,$3,clock_timestamp(),'hash','1980-01-01')`, id, label, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, adminID); err != nil {
		t.Fatal(err)
	}
	insertVerifiedDependentFixture(t, ctx, tx, guardianID, subjectID, "Jovem handoff", today.AddDate(-18, 0, 30).Format("2006-01-02"))
	if _, err = tx.Exec(ctx, `UPDATE users SET minor_login_id=$2,password_hash='existing-password-hash',credential_version=4 WHERE id=$1`, subjectID, "minor-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	seasonID := uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Handoff season',CURRENT_DATE-1,CURRENT_DATE+365)`, seasonID, "handoff-"+seasonID.String()[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on) SELECT $1,$2,id,CURRENT_DATE FROM programmes WHERE code='Leisure'`, subjectID, seasonID); err != nil {
		t.Fatal(err)
	}
	var initialUserCount int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&initialUserCount); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ReconcileGuardianAgeHandoffs(ctx); err != nil {
		t.Fatal(err)
	}
	handoff, err := q.GetGuardianAgeHandoffForSubject(ctx, subjectID)
	if err != nil {
		t.Fatal(err)
	}
	if handoff.Status != "PENDING" || handoff.Version != 1 {
		t.Fatalf("initial handoff=%+v", handoff)
	}
	if _, err = q.ListGuardianAgeHandoffsForAdmin(ctx, dbgen.ListGuardianAgeHandoffsForAdminParams{ActorID: adminID, RowLimit: 100, RowOffset: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err = q.GetGuardianAgeHandoffForAdmin(ctx, dbgen.GetGuardianAgeHandoffForAdminParams{ActorID: adminID, PublicRef: handoff.PublicRef}); err != nil {
		t.Fatal(err)
	}
	var queueViews, detailViews int
	if err = tx.QueryRow(ctx, `SELECT count(*) FILTER(WHERE view_kind='QUEUE'),count(*) FILTER(WHERE view_kind='DETAIL') FROM guardian_age_handoff_access_events WHERE actor_ref=$1`, adminID).Scan(&queueViews, &detailViews); err != nil {
		t.Fatal(err)
	}
	if queueViews != 1 || detailViews != 1 {
		t.Fatalf("access audit queue=%d detail=%d", queueViews, detailViews)
	}
	var guardianStatus string
	var guardianBirthday time.Time
	if err = tx.QueryRow(ctx, `SELECT status,birthday FROM guardian_age_handoff_for_guardian($1,$2)`, guardianID, subjectID).Scan(&guardianStatus, &guardianBirthday); err != nil || guardianStatus != "PENDING" || guardianBirthday.Format("2006-01-02") != today.AddDate(0, 0, 30).Format("2006-01-02") {
		t.Fatalf("guardian handoff status=%q birthday=%v err=%v", guardianStatus, guardianBirthday, err)
	}

	due, err := q.ListDueGuardianAgeHandoffNotices(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	var candidate dbgen.ListDueGuardianAgeHandoffNoticesRow
	for _, item := range due {
		if item.HandoffRef == handoff.PublicRef {
			candidate = item
		}
	}
	if candidate.HandoffRef == uuid.Nil || candidate.NoticeKind != "GUARDIAN_AGE_18_30_DAY" {
		t.Fatalf("due=%+v", due)
	}
	queued, err := q.EnqueueGuardianAgeHandoffNotice(ctx, dbgen.EnqueueGuardianAgeHandoffNoticeParams{HandoffRef: candidate.HandoffRef, RelationshipRef: candidate.RelationshipRef, GuardianUserID: candidate.GuardianUserID, Recipient: candidate.Recipient, RecipientVerifiedAt: candidate.RecipientVerifiedAt, Birthday: candidate.Birthday, NoticeKind: candidate.NoticeKind, SealedPayload: make([]byte, 64)})
	if err != nil || !queued {
		t.Fatalf("first queue=%v err=%v", queued, err)
	}
	queued, err = q.EnqueueGuardianAgeHandoffNotice(ctx, dbgen.EnqueueGuardianAgeHandoffNoticeParams{HandoffRef: candidate.HandoffRef, RelationshipRef: candidate.RelationshipRef, GuardianUserID: candidate.GuardianUserID, Recipient: candidate.Recipient, RecipientVerifiedAt: candidate.RecipientVerifiedAt, Birthday: candidate.Birthday, NoticeKind: candidate.NoticeKind, SealedPayload: make([]byte, 64)})
	if err != nil || queued {
		t.Fatalf("retry queue=%v err=%v", queued, err)
	}
	var handoffOutboxID uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT outbox.id FROM email_outbox outbox JOIN guardian_age_handoff_notices notice ON notice.id=outbox.guardian_handoff_notice_id WHERE notice.handoff_id=(SELECT id FROM guardian_age_handoffs WHERE public_ref=$1)`, handoff.PublicRef).Scan(&handoffOutboxID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET email=$2,email_verified_at=clock_timestamp() WHERE id=$1`, guardianID, "changed-"+uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	var deliverable bool
	if err = tx.QueryRow(ctx, `SELECT delivery.deliverable FROM guardian_age_handoff_outbox_delivery($1,clock_timestamp()) delivery`, handoffOutboxID).Scan(&deliverable); err != nil || deliverable {
		t.Fatalf("stale recipient deliverable=%v err=%v", deliverable, err)
	}

	personalEmail := "young-" + uuid.NewString() + "@example.test"
	tokenValue := sha256.Sum256([]byte(uuid.NewString()))
	tokenDigest := tokenValue[:]
	expires := time.Now().UTC().Add(24 * time.Hour)
	proposed, err := q.ProposeGuardianAgeHandoffEmail(ctx, dbgen.ProposeGuardianAgeHandoffEmailParams{ActorID: subjectID, ExpectedVersion: handoff.Version, Email: personalEmail, TokenDigest: tokenDigest, ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}, SealedPayload: make([]byte, 64)})
	if err != nil || proposed.Status != "EMAIL_PENDING" {
		t.Fatalf("proposed=%+v err=%v", proposed, err)
	}
	if _, err = q.VerifyGuardianAgeHandoffEmail(ctx, tokenDigest); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT reused_handoff_token`); err != nil {
		t.Fatal(err)
	}
	if _, err = q.VerifyGuardianAgeHandoffEmail(ctx, tokenDigest); err == nil {
		t.Fatal("one-use handoff token was accepted twice")
	}
	if _, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT reused_handoff_token`); rollbackErr != nil {
		t.Fatal(rollbackErr)
	}
	confirmed, err := q.ConfirmGuardianAgeHandoffIdentity(ctx, dbgen.ConfirmGuardianAgeHandoffIdentityParams{ActorID: adminID, PublicRef: handoff.PublicRef, ExpectedVersion: 3})
	if err != nil || confirmed.Status != "READY" || confirmed.Version != 4 {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT stale_handoff_decision`); err != nil {
		t.Fatal(err)
	}
	_, err = q.ConfirmGuardianAgeHandoffIdentity(ctx, dbgen.ConfirmGuardianAgeHandoffIdentityParams{ActorID: adminID, PublicRef: handoff.PublicRef, ExpectedVersion: 3})
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) || databaseError.Message != "guardian_age_handoff_stale" {
		t.Fatalf("same-version second decision err=%v", err)
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT stale_handoff_decision`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT repeated_handoff_decision`); err != nil {
		t.Fatal(err)
	}
	_, err = q.ConfirmGuardianAgeHandoffIdentity(ctx, dbgen.ConfirmGuardianAgeHandoffIdentityParams{ActorID: adminID, PublicRef: handoff.PublicRef, ExpectedVersion: confirmed.Version})
	if !errors.As(err, &databaseError) || databaseError.Message != "guardian_age_handoff_transition_rejected" {
		t.Fatalf("fresh-version repeated decision err=%v", err)
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT repeated_handoff_decision`); err != nil {
		t.Fatal(err)
	}
	var emailActor, adminActor uuid.UUID
	var emailAction, adminAction, method string
	if err = tx.QueryRow(ctx, `SELECT
		(SELECT actor_ref FROM guardian_age_handoff_events WHERE handoff_id=(SELECT id FROM guardian_age_handoffs WHERE public_ref=$1) AND action='EMAIL_VERIFIED'),
		(SELECT action FROM guardian_age_handoff_events WHERE handoff_id=(SELECT id FROM guardian_age_handoffs WHERE public_ref=$1) AND action='EMAIL_VERIFIED'),
		(SELECT actor_ref FROM guardian_age_handoff_events WHERE handoff_id=(SELECT id FROM guardian_age_handoffs WHERE public_ref=$1) AND action='IDENTITY_CONFIRMED'),
		(SELECT action FROM guardian_age_handoff_events WHERE handoff_id=(SELECT id FROM guardian_age_handoffs WHERE public_ref=$1) AND action='IDENTITY_CONFIRMED'),
		(SELECT method FROM guardian_age_handoff_events WHERE handoff_id=(SELECT id FROM guardian_age_handoffs WHERE public_ref=$1) AND action='IDENTITY_CONFIRMED')`, handoff.PublicRef).Scan(&emailActor, &emailAction, &adminActor, &adminAction, &method); err != nil {
		t.Fatal(err)
	}
	if emailActor != subjectID || emailAction != "EMAIL_VERIFIED" || adminActor != adminID || adminAction != "IDENTITY_CONFIRMED" || method != "CLUB_REGISTRATION_RECORD" {
		t.Fatalf("handoff audit email=%v/%s admin=%v/%s method=%s", emailActor, emailAction, adminActor, adminAction, method)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES($1,'\x',clock_timestamp()+interval '1 hour',$2,true)`, "handoff-"+uuid.NewString(), subjectID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET date_of_birth=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date WHERE id=$1`, subjectID); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ReconcileGuardianAgeHandoffs(ctx); err != nil {
		t.Fatal(err)
	}
	var sameID uuid.UUID
	var adult, active bool
	var email *string
	var password, login *string
	var credentialVersion int64
	var sessions, memberships, relationships, finalUserCount int
	if err = tx.QueryRow(ctx, `SELECT id,NOT is_dependent,is_active,email,password_hash,minor_login_id,credential_version FROM users WHERE id=$1`, subjectID).Scan(&sameID, &adult, &active, &email, &password, &login, &credentialVersion); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM sessions WHERE user_id=$1),(SELECT count(*) FROM user_memberships WHERE user_id=$1),(SELECT count(*) FROM guardian_authority_relationships WHERE subject_user_id=$1 AND state='EXPIRED')`, subjectID).Scan(&sessions, &memberships, &relationships); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&finalUserCount); err != nil {
		t.Fatal(err)
	}
	if sameID != subjectID || !adult || !active || email == nil || *email != personalEmail || password == nil || login != nil || credentialVersion != 5 || sessions != 0 || memberships != 1 || relationships != 1 || finalUserCount != initialUserCount {
		t.Fatalf("conversion id=%v adult=%v active=%v email=%v password=%v login=%v version=%d sessions=%d memberships=%d relationships=%d", sameID, adult, active, email, password, login, credentialVersion, sessions, memberships, relationships)
	}
	var status string
	var completed bool
	if err = tx.QueryRow(ctx, `SELECT status,completed_at IS NOT NULL FROM guardian_age_handoffs WHERE subject_user_id=$1`, subjectID).Scan(&status, &completed); err != nil || status != "COMPLETED" || !completed {
		t.Fatalf("status=%s completed=%v err=%v", status, completed, err)
	}
}

func TestGuardianAgeHandoffRejectsCollisionAndRelatedAdministrator(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	lisbon, _ := time.LoadLocation("Europe/Lisbon")
	today := time.Now().In(lisbon)
	guardianID, subjectID := uuid.New(), uuid.New()
	collisionEmail := "collision-" + uuid.NewString() + "@example.test"
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth) VALUES($1,'Related admin',$2,clock_timestamp(),'hash','1980-01-01'),($3,'Collision owner',$4,clock_timestamp(),'hash','1980-01-01')`, guardianID, uuid.NewString()+"@example.test", uuid.New(), collisionEmail); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, guardianID); err != nil {
		t.Fatal(err)
	}
	insertVerifiedDependentFixture(t, ctx, tx, guardianID, subjectID, "Jovem collision", today.AddDate(-18, 0, 20).Format("2006-01-02"))
	if _, err = tx.Exec(ctx, `UPDATE users SET minor_login_id=$2,password_hash='hash' WHERE id=$1`, subjectID, "minor-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ReconcileGuardianAgeHandoffs(ctx); err != nil {
		t.Fatal(err)
	}
	handoff, err := q.GetGuardianAgeHandoffForSubject(ctx, subjectID)
	if err != nil {
		t.Fatal(err)
	}
	tokenValue := sha256.Sum256([]byte(uuid.NewString()))
	if _, err = tx.Exec(ctx, `SAVEPOINT handoff_collision`); err != nil {
		t.Fatal(err)
	}
	_, err = q.ProposeGuardianAgeHandoffEmail(ctx, dbgen.ProposeGuardianAgeHandoffEmailParams{ActorID: subjectID, ExpectedVersion: handoff.Version, Email: collisionEmail, TokenDigest: tokenValue[:], ExpiresAt: pgtype.Timestamptz{Time: time.Now().UTC().Add(24 * time.Hour), Valid: true}, SealedPayload: make([]byte, 64)})
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) || databaseError.Message != "guardian_age_handoff_email_collision" {
		t.Fatalf("collision err=%v", err)
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT handoff_collision`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT related_handoff_admin`); err != nil {
		t.Fatal(err)
	}
	_, err = q.ConfirmGuardianAgeHandoffIdentity(ctx, dbgen.ConfirmGuardianAgeHandoffIdentityParams{ActorID: guardianID, PublicRef: handoff.PublicRef, ExpectedVersion: handoff.Version})
	if !errors.As(err, &databaseError) || databaseError.Message != "guardian_age_handoff_separation_required" {
		t.Fatalf("related admin err=%v", err)
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT related_handoff_admin`); err != nil {
		t.Fatal(err)
	}
	var eventCount int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM guardian_age_handoff_events event JOIN guardian_age_handoffs handoff ON handoff.id=event.handoff_id WHERE handoff.public_ref=$1`, handoff.PublicRef).Scan(&eventCount); err != nil || eventCount != 1 {
		t.Fatalf("events=%d err=%v", eventCount, err)
	}
}

func TestGuardianAgeHandoffCollisionCanBeConfirmedThenRecoveredOnSameIdentity(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	lisbon, _ := time.LoadLocation("Europe/Lisbon")
	today := time.Now().In(lisbon)
	guardianID, subjectID, adminID := uuid.New(), uuid.New(), uuid.New()
	for id, label := range map[uuid.UUID]string{guardianID: "Responsável collision recovery", adminID: "Admin collision recovery"} {
		if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth) VALUES($1,$2,$3,clock_timestamp(),'hash','1980-01-01')`, id, label, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, adminID); err != nil {
		t.Fatal(err)
	}
	insertVerifiedDependentFixture(t, ctx, tx, guardianID, subjectID, "Jovem collision recoverable", today.AddDate(-18, 0, 5).Format("2006-01-02"))
	if _, err = tx.Exec(ctx, `UPDATE users SET minor_login_id=$2,password_hash='preserved-hash' WHERE id=$1`, subjectID, "minor-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ReconcileGuardianAgeHandoffs(ctx); err != nil {
		t.Fatal(err)
	}
	handoff, err := q.GetGuardianAgeHandoffForSubject(ctx, subjectID)
	if err != nil {
		t.Fatal(err)
	}
	collidingEmail := "later-collision-" + uuid.NewString() + "@example.test"
	token := sha256.Sum256([]byte(uuid.NewString()))
	_, err = q.ProposeGuardianAgeHandoffEmail(ctx, dbgen.ProposeGuardianAgeHandoffEmailParams{ActorID: subjectID, ExpectedVersion: handoff.Version, Email: collidingEmail, TokenDigest: token[:], ExpiresAt: pgtype.Timestamptz{Time: time.Now().UTC().Add(24 * time.Hour), Valid: true}, SealedPayload: make([]byte, 64)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.VerifyGuardianAgeHandoffEmail(ctx, token[:]); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth) VALUES($1,'Collision owner',$2,clock_timestamp(),'hash','1980-01-01')`, uuid.New(), collidingEmail); err != nil {
		t.Fatal(err)
	}
	var initialUserCount int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&initialUserCount); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET date_of_birth=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date WHERE id=$1`, subjectID); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ReconcileGuardianAgeHandoffs(ctx); err != nil {
		t.Fatal(err)
	}
	collided, err := q.GetGuardianAgeHandoffForSubject(ctx, subjectID)
	if err != nil || collided.Status != "EMAIL_COLLISION" {
		t.Fatalf("collision=%+v err=%v", collided, err)
	}
	confirmed, err := q.ConfirmGuardianAgeHandoffIdentity(ctx, dbgen.ConfirmGuardianAgeHandoffIdentityParams{ActorID: adminID, PublicRef: collided.PublicRef, ExpectedVersion: collided.Version})
	if err != nil || confirmed.Status != "EMAIL_COLLISION" || !confirmed.IdentityConfirmedAt.Valid {
		t.Fatalf("confirmed collision=%+v err=%v", confirmed, err)
	}
	recoveryEmail := "collision-recovered-" + uuid.NewString() + "@example.test"
	recoveryToken := sha256.Sum256([]byte(uuid.NewString()))
	recovered, err := q.RecoverGuardianAgeHandoffEmail(ctx, dbgen.RecoverGuardianAgeHandoffEmailParams{ActorID: adminID, PublicRef: collided.PublicRef, ExpectedVersion: confirmed.Version, Email: recoveryEmail, TokenDigest: recoveryToken[:], ExpiresAt: pgtype.Timestamptz{Time: time.Now().UTC().Add(24 * time.Hour), Valid: true}, SealedPayload: make([]byte, 64)})
	if err != nil || recovered.Status != "EMAIL_PENDING" {
		t.Fatalf("recovered=%+v err=%v", recovered, err)
	}
	if _, err = q.VerifyGuardianAgeHandoffEmail(ctx, recoveryToken[:]); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ReconcileGuardianAgeHandoffs(ctx); err != nil {
		t.Fatal(err)
	}
	var finalID uuid.UUID
	var finalEmail string
	var dependent bool
	var finalUserCount int
	if err = tx.QueryRow(ctx, `SELECT id,email::text,is_dependent FROM users WHERE id=$1`, subjectID).Scan(&finalID, &finalEmail, &dependent); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&finalUserCount); err != nil {
		t.Fatal(err)
	}
	if finalID != subjectID || finalEmail != recoveryEmail || dependent || finalUserCount != initialUserCount {
		t.Fatalf("identity=%v email=%q dependent=%v users=%d want=%d", finalID, finalEmail, dependent, finalUserCount, initialUserCount)
	}
}

func TestGuardianAgeHandoffFirstBirthdayReconciliationMaterializesRecoveryBeforeCutoff(t *testing.T) {
	for _, daysAfterBirthday := range []int{0, 1} {
		t.Run(strconv.Itoa(daysAfterBirthday)+" days after birthday", func(t *testing.T) {
			ctx := context.Background()
			conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close(ctx)
			tx, err := conn.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(ctx)
			q := dbgen.New(tx)
			guardianID, subjectID := uuid.New(), uuid.New()
			if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth) VALUES($1,'Responsável first reconcile',$2,clock_timestamp(),'hash','1980-01-01')`, guardianID, uuid.NewString()+"@example.test"); err != nil {
				t.Fatal(err)
			}
			lisbon, err := time.LoadLocation("Europe/Lisbon")
			if err != nil {
				t.Fatal(err)
			}
			born := time.Now().In(lisbon).AddDate(-18, 0, -daysAfterBirthday).Format("2006-01-02")
			insertVerifiedDependentFixture(t, ctx, tx, guardianID, subjectID, "Jovem first reconcile", born)
			login := "minor-" + uuid.NewString()
			if _, err = tx.Exec(ctx, `UPDATE users SET minor_login_id=$2,password_hash='preserved-hash',credential_version=8 WHERE id=$1`, subjectID, login); err != nil {
				t.Fatal(err)
			}
			if _, err = tx.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES($1,'\x',clock_timestamp()+interval '1 hour',$2,true)`, "first-"+uuid.NewString(), subjectID); err != nil {
				t.Fatal(err)
			}
			if _, err = q.ReconcileGuardianAuthorityCutoffs(ctx); err != nil {
				t.Fatal(err)
			}
			var status, password, storedLogin string
			var credentialVersion int64
			var sessions, relationships int
			if err = tx.QueryRow(ctx, `SELECT handoff.status,subject.password_hash,subject.minor_login_id,subject.credential_version,
				(SELECT count(*) FROM sessions WHERE user_id=subject.id),(SELECT count(*) FROM guardian_authority_relationships WHERE subject_user_id=subject.id AND state='EXPIRED')
				FROM guardian_age_handoffs handoff JOIN users subject ON subject.id=handoff.subject_user_id WHERE subject.id=$1`, subjectID).
				Scan(&status, &password, &storedLogin, &credentialVersion, &sessions, &relationships); err != nil {
				t.Fatal(err)
			}
			if status != "RECOVERY_REQUIRED" || password != "preserved-hash" || storedLogin != login || credentialVersion != 9 || sessions != 0 || relationships != 1 {
				t.Fatalf("status=%s password=%q login=%q version=%d sessions=%d relationships=%d", status, password, storedLogin, credentialVersion, sessions, relationships)
			}
		})
	}
}

func TestGuardianAgeHandoffIncompleteAccountUsesSameIdentityRecovery(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	q := dbgen.New(tx)
	lisbon, _ := time.LoadLocation("Europe/Lisbon")
	today := time.Now().In(lisbon)
	guardianID, subjectID, adminID := uuid.New(), uuid.New(), uuid.New()
	login := "minor-" + uuid.NewString()
	for id, label := range map[uuid.UUID]string{guardianID: "Responsável recovery", adminID: "Admin recovery"} {
		if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth) VALUES($1,$2,$3,clock_timestamp(),'hash','1980-01-01')`, id, label, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, adminID); err != nil {
		t.Fatal(err)
	}
	insertVerifiedDependentFixture(t, ctx, tx, guardianID, subjectID, "Jovem recovery", today.AddDate(-18, 0, 1).Format("2006-01-02"))
	if _, err = tx.Exec(ctx, `UPDATE users SET minor_login_id=$2,password_hash='preserved-hash',credential_version=2 WHERE id=$1`, subjectID, login); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO sessions(token,data,expiry,user_id,subject_indexed) VALUES($1,'\x',clock_timestamp()+interval '1 hour',$2,true)`, "recovery-"+uuid.NewString(), subjectID); err != nil {
		t.Fatal(err)
	}
	var initialUserCount int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&initialUserCount); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ReconcileGuardianAgeHandoffs(ctx); err != nil {
		t.Fatal(err)
	}
	handoff, err := q.GetGuardianAgeHandoffForSubject(ctx, subjectID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE users SET date_of_birth=((CURRENT_TIMESTAMP AT TIME ZONE 'Europe/Lisbon')::date-interval '18 years')::date WHERE id=$1`, subjectID); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ReconcileGuardianAgeHandoffs(ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	var password, storedLogin *string
	var sessions int
	var version int64
	if err = tx.QueryRow(ctx, `SELECT handoff.status,handoff.version,subject.password_hash,subject.minor_login_id,(SELECT count(*) FROM sessions WHERE user_id=subject.id) FROM guardian_age_handoffs handoff JOIN users subject ON subject.id=handoff.subject_user_id WHERE subject.id=$1`, subjectID).Scan(&status, &version, &password, &storedLogin, &sessions); err != nil {
		t.Fatal(err)
	}
	if status != "RECOVERY_REQUIRED" || version != 2 || password == nil || storedLogin == nil || sessions != 0 {
		t.Fatalf("status=%s version=%d password=%v login=%v sessions=%d", status, version, password, storedLogin, sessions)
	}
	if _, err = q.GetActiveDependentByLoginID(ctx, &login); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("majority login remained active: %v", err)
	}
	confirmed, err := q.ConfirmGuardianAgeHandoffIdentity(ctx, dbgen.ConfirmGuardianAgeHandoffIdentityParams{ActorID: adminID, PublicRef: handoff.PublicRef, ExpectedVersion: version})
	if err != nil || confirmed.Status != "RECOVERY_REQUIRED" {
		t.Fatalf("confirmed=%+v err=%v", confirmed, err)
	}
	personalEmail := "recovered-" + uuid.NewString() + "@example.test"
	token := sha256.Sum256([]byte(uuid.NewString()))
	expires := time.Now().UTC().Add(24 * time.Hour)
	recovered, err := q.RecoverGuardianAgeHandoffEmail(ctx, dbgen.RecoverGuardianAgeHandoffEmailParams{ActorID: adminID, PublicRef: handoff.PublicRef, ExpectedVersion: confirmed.Version, Email: personalEmail, TokenDigest: token[:], ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}, SealedPayload: make([]byte, 64)})
	if err != nil || recovered.Status != "EMAIL_PENDING" {
		t.Fatalf("recovered=%+v err=%v", recovered, err)
	}
	if _, err = q.VerifyGuardianAgeHandoffEmail(ctx, token[:]); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ReconcileGuardianAgeHandoffs(ctx); err != nil {
		t.Fatal(err)
	}
	var adult bool
	var finalEmail *string
	var finalID uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT id,NOT is_dependent,email FROM users WHERE id=$1`, subjectID).Scan(&finalID, &adult, &finalEmail); err != nil {
		t.Fatal(err)
	}
	var finalUserCount int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&finalUserCount); err != nil {
		t.Fatal(err)
	}
	if finalID != subjectID || !adult || finalEmail == nil || *finalEmail != personalEmail || finalUserCount != initialUserCount {
		t.Fatalf("identity=%v adult=%v email=%v", finalID, adult, finalEmail)
	}
}

func TestGuardianAgeHandoffGuardianNoticesAreExactlyOnceAndRaceSafe(t *testing.T) {
	t.Run("seven day follows existing thirty day exactly once", func(t *testing.T) {
		ctx := context.Background()
		conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close(ctx)
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		q := dbgen.New(tx)
		guardianID, _, handoffRef := insertAgeHandoffNoticeFixture(t, ctx, tx, 7)
		var handoffID, relationshipID uuid.UUID
		var relationshipRef uuid.UUID
		var birthday time.Time
		if err = tx.QueryRow(ctx, `SELECT handoff.id,relationship.id,relationship.public_ref,(subject.date_of_birth+interval '18 years')::date
			FROM guardian_age_handoffs handoff JOIN users subject ON subject.id=handoff.subject_user_id
			JOIN guardian_authority_relationships relationship ON relationship.subject_user_id=subject.id
			WHERE handoff.public_ref=$1 AND relationship.guardian_user_id=$2`, handoffRef, guardianID).Scan(&handoffID, &relationshipID, &relationshipRef, &birthday); err != nil {
			t.Fatal(err)
		}
		var thirtyNoticeID uuid.UUID
		if err = tx.QueryRow(ctx, `INSERT INTO guardian_age_handoff_notices(handoff_id,relationship_id,guardian_user_id,recipient_verified_at,audience,notice_kind,birthday)
			SELECT $1,$2,id,email_verified_at,'GUARDIAN_EMAIL','GUARDIAN_AGE_18_30_DAY',$3 FROM users WHERE id=$4 RETURNING id`, handoffID, relationshipID, birthday, guardianID).Scan(&thirtyNoticeID); err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, `INSERT INTO email_outbox(message_type,sealed_payload,guardian_handoff_notice_id,next_attempt_at) VALUES('GUARDIAN_AGE_18_30_DAY',$1,$2,clock_timestamp())`, make([]byte, 64), thirtyNoticeID); err != nil {
			t.Fatal(err)
		}
		due, err := q.ListDueGuardianAgeHandoffNotices(ctx, 100)
		if err != nil {
			t.Fatal(err)
		}
		var seven dbgen.ListDueGuardianAgeHandoffNoticesRow
		for _, item := range due {
			if item.HandoffRef == handoffRef {
				seven = item
			}
		}
		if seven.NoticeKind != "GUARDIAN_AGE_18_7_DAY" || seven.RelationshipRef != relationshipRef {
			t.Fatalf("seven-day candidate=%+v", seven)
		}
		params := dbgen.EnqueueGuardianAgeHandoffNoticeParams{HandoffRef: seven.HandoffRef, RelationshipRef: seven.RelationshipRef, GuardianUserID: seven.GuardianUserID, Recipient: seven.Recipient, RecipientVerifiedAt: seven.RecipientVerifiedAt, Birthday: seven.Birthday, NoticeKind: seven.NoticeKind, SealedPayload: make([]byte, 64)}
		if queued, err := q.EnqueueGuardianAgeHandoffNotice(ctx, params); err != nil || !queued {
			t.Fatalf("seven-day queue=%v err=%v", queued, err)
		}
		if queued, err := q.EnqueueGuardianAgeHandoffNotice(ctx, params); err != nil || queued {
			t.Fatalf("seven-day retry=%v err=%v", queued, err)
		}
		var thirtyCount, sevenCount, outboxCount int
		if err = tx.QueryRow(ctx, `SELECT
			count(*) FILTER(WHERE notice_kind='GUARDIAN_AGE_18_30_DAY'),
			count(*) FILTER(WHERE notice_kind='GUARDIAN_AGE_18_7_DAY'),
			(SELECT count(*) FROM email_outbox outbox WHERE outbox.guardian_handoff_notice_id IN(SELECT id FROM guardian_age_handoff_notices WHERE handoff_id=$1))
			FROM guardian_age_handoff_notices WHERE handoff_id=$1 AND audience='GUARDIAN_EMAIL'`, handoffID).Scan(&thirtyCount, &sevenCount, &outboxCount); err != nil {
			t.Fatal(err)
		}
		if thirtyCount != 1 || sevenCount != 1 || outboxCount != 2 {
			t.Fatalf("notice counts 30=%d 7=%d outbox=%d", thirtyCount, sevenCount, outboxCount)
		}
	})

	t.Run("recipient change between discovery and enqueue fails closed", func(t *testing.T) {
		ctx := context.Background()
		conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close(ctx)
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		q := dbgen.New(tx)
		guardianID, _, handoffRef := insertAgeHandoffNoticeFixture(t, ctx, tx, 20)
		due, err := q.ListDueGuardianAgeHandoffNotices(ctx, 100)
		if err != nil {
			t.Fatal(err)
		}
		var candidate dbgen.ListDueGuardianAgeHandoffNoticesRow
		for _, item := range due {
			if item.HandoffRef == handoffRef {
				candidate = item
			}
		}
		if candidate.HandoffRef == uuid.Nil {
			t.Fatal("missing guardian notice candidate")
		}
		if _, err = tx.Exec(ctx, `UPDATE users SET email=$2,email_verified_at=clock_timestamp() WHERE id=$1`, guardianID, "race-"+uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
		queued, err := q.EnqueueGuardianAgeHandoffNotice(ctx, dbgen.EnqueueGuardianAgeHandoffNoticeParams{HandoffRef: candidate.HandoffRef, RelationshipRef: candidate.RelationshipRef, GuardianUserID: candidate.GuardianUserID, Recipient: candidate.Recipient, RecipientVerifiedAt: candidate.RecipientVerifiedAt, Birthday: candidate.Birthday, NoticeKind: candidate.NoticeKind, SealedPayload: make([]byte, 64)})
		if err != nil || queued {
			t.Fatalf("stale discovery queue=%v err=%v", queued, err)
		}
		var guardianNoticeCount int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM guardian_age_handoff_notices notice JOIN guardian_age_handoffs handoff ON handoff.id=notice.handoff_id WHERE handoff.public_ref=$1 AND notice.audience='GUARDIAN_EMAIL'`, handoffRef).Scan(&guardianNoticeCount); err != nil || guardianNoticeCount != 0 {
			t.Fatalf("guardian notices=%d err=%v", guardianNoticeCount, err)
		}
	})
}

func TestGuardianAgeHandoffConcurrentEnqueueHasExactlyOneWinner(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	guardianID, _, handoffRef := insertAgeHandoffNoticeFixture(t, ctx, conn, 20)
	defer func() {
		_, _ = conn.Exec(context.Background(), `UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled`)
	}()
	due, err := dbgen.New(conn).ListDueGuardianAgeHandoffNotices(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	var candidate dbgen.ListDueGuardianAgeHandoffNoticesRow
	for _, item := range due {
		if item.HandoffRef == handoffRef && item.GuardianUserID == guardianID {
			candidate = item
		}
	}
	if candidate.HandoffRef == uuid.Nil {
		t.Fatal("missing concurrent guardian notice candidate")
	}
	params := dbgen.EnqueueGuardianAgeHandoffNoticeParams{HandoffRef: candidate.HandoffRef, RelationshipRef: candidate.RelationshipRef, GuardianUserID: candidate.GuardianUserID, Recipient: candidate.Recipient, RecipientVerifiedAt: candidate.RecipientVerifiedAt, Birthday: candidate.Birthday, NoticeKind: candidate.NoticeKind, SealedPayload: make([]byte, 64)}
	type result struct {
		queued bool
		err    error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			parallel, openErr := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
			if openErr != nil {
				results <- result{err: openErr}
				return
			}
			defer parallel.Close(ctx)
			queued, queueErr := dbgen.New(parallel).EnqueueGuardianAgeHandoffNotice(ctx, params)
			results <- result{queued: queued, err: queueErr}
		}()
	}
	wg.Wait()
	close(results)
	winners, losers := 0, 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.queued {
			winners++
		} else {
			losers++
		}
	}
	var notices, outbox int
	if err = conn.QueryRow(ctx, `SELECT count(*) FILTER(WHERE notice.audience='GUARDIAN_EMAIL'),(SELECT count(*) FROM email_outbox outbox WHERE outbox.guardian_handoff_notice_id IN(SELECT id FROM guardian_age_handoff_notices WHERE handoff_id=handoff.id)) FROM guardian_age_handoffs handoff JOIN guardian_age_handoff_notices notice ON notice.handoff_id=handoff.id WHERE handoff.public_ref=$1 GROUP BY handoff.id`, handoffRef).Scan(&notices, &outbox); err != nil {
		t.Fatal(err)
	}
	if winners != 1 || losers != 1 || notices != 1 || outbox != 1 {
		t.Fatalf("winners=%d losers=%d notices=%d outbox=%d", winners, losers, notices, outbox)
	}
}

func TestGuardianAgeHandoffConcurrentAdminConfirmationHasOneSafeLoser(t *testing.T) {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	guardianID, subjectID, adminOne, adminTwo := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for id, label := range map[uuid.UUID]string{guardianID: "Responsável handoff concurrency", adminOne: "Admin handoff concurrency one", adminTwo: "Admin handoff concurrency two"} {
		if _, err = conn.Exec(ctx, `INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth) VALUES($1,$2,$3,clock_timestamp(),'hash','1980-01-01')`, id, label, uuid.NewString()+"@example.test"); err != nil {
			t.Fatal(err)
		}
	}
	for _, adminID := range []uuid.UUID{adminOne, adminTwo} {
		if _, err = conn.Exec(ctx, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, adminID); err != nil {
			t.Fatal(err)
		}
	}
	lisbon, _ := time.LoadLocation("Europe/Lisbon")
	insertVerifiedDependentFixture(t, ctx, conn, guardianID, subjectID, "Jovem handoff concurrency", time.Now().In(lisbon).AddDate(-18, 0, 20).Format("2006-01-02"))
	defer func() {
		_, _ = conn.Exec(context.Background(), `UPDATE guardian_authority_policies SET enabled=false,enabled_at=NULL,enabled_by=NULL WHERE enabled`)
	}()
	if _, err = conn.Exec(ctx, `UPDATE users SET minor_login_id=$2,password_hash='hash' WHERE id=$1`, subjectID, "minor-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	q := dbgen.New(conn)
	if _, err = q.ReconcileGuardianAgeHandoffs(ctx); err != nil {
		t.Fatal(err)
	}
	handoff, err := q.GetGuardianAgeHandoffForSubject(ctx, subjectID)
	if err != nil {
		t.Fatal(err)
	}
	type result struct{ err error }
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, adminID := range []uuid.UUID{adminOne, adminTwo} {
		wg.Add(1)
		go func(actorID uuid.UUID) {
			defer wg.Done()
			parallel, openErr := pgx.Connect(ctx, os.Getenv("TEST_DATABASE_URL"))
			if openErr != nil {
				results <- result{err: openErr}
				return
			}
			defer parallel.Close(ctx)
			_, transitionErr := dbgen.New(parallel).ConfirmGuardianAgeHandoffIdentity(ctx, dbgen.ConfirmGuardianAgeHandoffIdentityParams{ActorID: actorID, PublicRef: handoff.PublicRef, ExpectedVersion: handoff.Version})
			results <- result{err: transitionErr}
		}(adminID)
	}
	wg.Wait()
	close(results)
	successes, stale := 0, 0
	for result := range results {
		if result.err == nil {
			successes++
			continue
		}
		var databaseError *pgconn.PgError
		if errors.As(result.err, &databaseError) && databaseError.Message == "guardian_age_handoff_stale" {
			stale++
			continue
		}
		t.Fatalf("unexpected concurrent result: %v", result.err)
	}
	var confirmationEvents int
	if err = conn.QueryRow(ctx, `SELECT count(*) FROM guardian_age_handoff_events event JOIN guardian_age_handoffs handoff ON handoff.id=event.handoff_id WHERE handoff.public_ref=$1 AND event.action='IDENTITY_CONFIRMED'`, handoff.PublicRef).Scan(&confirmationEvents); err != nil {
		t.Fatal(err)
	}
	if successes != 1 || stale != 1 || confirmationEvents != 1 {
		t.Fatalf("successes=%d stale=%d confirmation events=%d", successes, stale, confirmationEvents)
	}
	confirmed, err := q.GetGuardianAgeHandoffForSubject(ctx, subjectID)
	if err != nil || confirmed.Status != "IDENTITY_CONFIRMED" {
		t.Fatalf("confirmed handoff=%+v err=%v", confirmed, err)
	}
	forgedToken := sha256.Sum256([]byte(uuid.NewString()))
	_, err = q.RecoverGuardianAgeHandoffEmail(ctx, dbgen.RecoverGuardianAgeHandoffEmailParams{ActorID: adminOne, PublicRef: handoff.PublicRef, ExpectedVersion: confirmed.Version, Email: "premature-" + uuid.NewString() + "@example.test", TokenDigest: forgedToken[:], ExpiresAt: pgtype.Timestamptz{Time: time.Now().UTC().Add(24 * time.Hour), Valid: true}, SealedPayload: make([]byte, 64)})
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) || databaseError.Message != "guardian_age_handoff_recovery_rejected" {
		t.Fatalf("pre-birthday recovery err=%v", err)
	}
	afterRejected, err := q.GetGuardianAgeHandoffForSubject(ctx, subjectID)
	if err != nil || afterRejected.Version != confirmed.Version || afterRejected.ProposedEmail != "" {
		t.Fatalf("forged recovery mutated handoff=%+v err=%v", afterRejected, err)
	}
}

func insertAgeHandoffNoticeFixture(t *testing.T, ctx context.Context, tx dbgen.DBTX, daysUntilBirthday int) (uuid.UUID, uuid.UUID, uuid.UUID) {
	t.Helper()
	lisbon, err := time.LoadLocation("Europe/Lisbon")
	if err != nil {
		t.Fatal(err)
	}
	guardianID, subjectID := uuid.New(), uuid.New()
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth) VALUES($1,'Responsável notice',$2,clock_timestamp(),'hash','1980-01-01')`, guardianID, uuid.NewString()+"@example.test"); err != nil {
		t.Fatal(err)
	}
	today := time.Now().In(lisbon)
	insertVerifiedDependentFixture(t, ctx, tx, guardianID, subjectID, "Jovem notice", today.AddDate(-18, 0, daysUntilBirthday).Format("2006-01-02"))
	if _, err = tx.Exec(ctx, `UPDATE users SET minor_login_id=$2,password_hash='hash' WHERE id=$1`, subjectID, "minor-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err = dbgen.New(tx).ReconcileGuardianAgeHandoffs(ctx); err != nil {
		t.Fatal(err)
	}
	var handoffRef uuid.UUID
	if err = tx.QueryRow(ctx, `SELECT public_ref FROM guardian_age_handoffs WHERE subject_user_id=$1`, subjectID).Scan(&handoffRef); err != nil {
		t.Fatal(err)
	}
	return guardianID, subjectID, handoffRef
}
