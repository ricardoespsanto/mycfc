//go:build integration

package db

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	dbgen "github.com/cfcoimbra/mycfc/internal/db/generated"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Real committed corrections are followed by fresh reads on every audience surface.
// Run only against disposable integration databases, as with the other sporting fixtures.
func TestAnnouncementSportingAudienceCorrections(t *testing.T) {
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, os.Getenv("TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	q := dbgen.New(pool)
	admin, coach, athlete, guardian, child := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{admin, coach, athlete, guardian} {
		exec(`INSERT INTO users(id,name,email,email_verified_at,password_hash,date_of_birth) VALUES($1,'Audience fixture',$2,now(),'hash','1990-01-01')`, id, id.String()+"@example.test")
	}
	exec(`INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, admin)
	insertVerifiedDependentFixture(t, ctx, pool, guardian, child, "Audience child", "2015-01-01")
	var programme uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&programme); err != nil {
		t.Fatal(err)
	}
	season, team, membership, childMembership := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	exec(`INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,$2,'Audience season',CURRENT_DATE-30,CURRENT_DATE+30)`, season, "A_"+uuid.NewString()[:8])
	exec(`INSERT INTO teams(id,programme_id,season_id,code,name) VALUES($1,$2,$3,'Audience','Audience team')`, team, programme, season)
	for user, mid := range map[uuid.UUID]uuid.UUID{athlete: membership, child: childMembership} {
		exec(`INSERT INTO user_memberships(id,user_id,season_id,programme_id,team_id,starts_on) VALUES($1,$2,$3,$4,$5,CURRENT_DATE-2)`, mid, user, season, programme, team)
	}
	exec(`INSERT INTO staff_grants(user_id,capability,programme_id,granted_by_id) VALUES($1,'COACH',$2,$3)`, coach, programme, admin)
	legacy := map[string]uuid.UUID{}
	rows, err := pool.Query(ctx, `SELECT code,id FROM modalities`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var c string
		var id uuid.UUID
		if err := rows.Scan(&c, &id); err != nil {
			t.Fatal(err)
		}
		legacy[c] = id
	}
	rows.Close()
	exec(`INSERT INTO membership_modalities(membership_id,modality_id) VALUES($1,$2)`, membership, legacy["K1"])
	all := map[uuid.UUID]bool{}
	create := func(kind dbgen.AnnouncementTargetType, id *uuid.UUID, guard bool) uuid.UUID {
		t.Helper()
		a, err := q.CreateAnnouncement(ctx, dbgen.CreateAnnouncementParams{Title: "Audience fixture", Body: "Retained body", AuthorID: admin})
		if err != nil {
			t.Fatal(err)
		}
		all[a.ID] = true
		if kind != "" {
			if err := q.AddAnnouncementTarget(ctx, dbgen.AddAnnouncementTargetParams{AnnouncementID: a.ID, TargetType: kind, TargetID: id}); err != nil {
				t.Fatal(err)
			}
		}
		if guard {
			if err := q.AddAnnouncementTarget(ctx, dbgen.AddAnnouncementTargetParams{AnnouncementID: a.ID, TargetType: dbgen.AnnouncementTargetTypeGUARDIAN}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := q.PublishAnnouncement(ctx, dbgen.PublishAnnouncementParams{ID: a.ID, ActorUserID: &admin}); err != nil {
			t.Fatal(err)
		}
		return a.ID
	}
	crafts := []string{"K1", "K2", "K4", "C1", "C2", "C4"}
	targets := map[string]uuid.UUID{}
	for _, c := range append(append([]string{}, crafts...), "SUP") {
		id := legacy[c]
		targets[c] = create(dbgen.AnnouncementTargetTypeMODALITY, &id, false)
	}
	unknown := uuid.New()
	_ = create(dbgen.AnnouncementTargetTypeMODALITY, &unknown, false)
	unknownCode := uuid.New()
	exec(`INSERT INTO modalities(id,code,name_pt) VALUES($1,$2,'Unknown fixture')`, unknownCode, "U"+strings.ToUpper(uuid.NewString()[:8]))
	_ = create(dbgen.AnnouncementTargetTypeMODALITY, &unknownCode, false)
	k1 := legacy["K1"]
	mixed := create(dbgen.AnnouncementTargetTypeMODALITY, &k1, false)
	if err := q.AddAnnouncementTarget(ctx, dbgen.AddAnnouncementTargetParams{AnnouncementID: mixed, TargetType: dbgen.AnnouncementTargetTypeTEAM, TargetID: &team}); err != nil {
		t.Fatal(err)
	}
	guarded := create(dbgen.AnnouncementTargetTypeMODALITY, &k1, true)
	guardedMixed := create(dbgen.AnnouncementTargetTypeMODALITY, &k1, true)
	if err := q.AddAnnouncementTarget(ctx, dbgen.AddAnnouncementTargetParams{AnnouncementID: guardedMixed, TargetType: dbgen.AnnouncementTargetTypeTEAM, TargetID: &team}); err != nil {
		t.Fatal(err)
	}
	draft, err := q.CreateAnnouncement(ctx, dbgen.CreateAnnouncementParams{Title: "Draft fixture", Body: "Not published", AuthorID: admin})
	if err != nil {
		t.Fatal(err)
	}
	all[draft.ID] = true
	elapsed := uuid.New()
	all[elapsed] = true
	exec(`INSERT INTO announcements(id,title,body,status,author_id,published_by_id,published_at,expires_at) VALUES($1,'Elapsed fixture','Retained expired body','PUBLISHED',$2,$2,now()-interval '2 hours',now()-interval '1 hour')`, elapsed, admin)
	guardianOnly := create("", nil, true)
	// A second verified child shares the same operational policy, but has no sport/participation.
	otherChild := uuid.New()
	exec(`INSERT INTO users(id,name,is_dependent,date_of_birth) VALUES($1,'Other child',true,'2014-01-01')`, otherChild)
	exec(`INSERT INTO guardian_authority_relationships(guardian_user_id,subject_user_id,submitted_label,state,version,policy_version,verified_at,verified_by,verified_until,review_due_at) SELECT guardian_user_id,$2,'Other child','VERIFIED',2,policy_version,verified_at,verified_by,verified_until,review_due_at FROM guardian_authority_relationships WHERE subject_user_id=$1`, child, otherChild)
	baseline := map[uuid.UUID]int64{}
	for _, user := range []uuid.UUID{athlete, guardian} {
		n, err := q.CountUnreadVisibleAnnouncements(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		baseline[user] = n
	}
	// Baselines include any non-fixture announcements; subtract currently visible fixture rows.
	for _, user := range []uuid.UUID{athlete, guardian} {
		list, err := q.ListVisibleAnnouncements(ctx, dbgen.ListVisibleAnnouncementsParams{UserID: user, RowLimit: 10000})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range list {
			if all[r.ID] && !r.ReadAt.Valid {
				baseline[user]--
			}
		}
	}
	read := map[uuid.UUID]bool{}
	assert := func(user uuid.UUID, want ...uuid.UUID) {
		t.Helper()
		expected := map[uuid.UUID]bool{}
		for _, id := range want {
			expected[id] = true
		}
		list, err := q.ListVisibleAnnouncements(ctx, dbgen.ListVisibleAnnouncementsParams{UserID: user, RowLimit: 10000})
		if err != nil {
			t.Fatal(err)
		}
		found := map[uuid.UUID]bool{}
		unread := baseline[user]
		for _, r := range list {
			if all[r.ID] {
				if found[r.ID] {
					t.Fatal("duplicate announcement")
				}
				if r.ReadAt.Valid != read[r.ID] {
					t.Fatal("list lost current read state")
				}
				found[r.ID] = true
				if !read[r.ID] {
					unread++
				}
			}
		}
		if !reflect.DeepEqual(found, expected) {
			t.Fatalf("user=%s visible=%v want=%v", user, found, expected)
		}
		for id := range all {
			detail, err := q.GetVisibleAnnouncement(ctx, dbgen.GetVisibleAnnouncementParams{UserID: user, ID: id})
			if expected[id] {
				if err != nil || detail.ID != id || detail.ReadAt.Valid != read[id] {
					t.Fatalf("detail %s: %v", id, err)
				}
			} else if !errors.Is(err, pgx.ErrNoRows) {
				t.Fatalf("hidden detail %s: %v", id, err)
			}
		}
		n, err := q.CountUnreadVisibleAnnouncements(ctx, user)
		if err != nil || n != unread {
			t.Fatalf("unread=%d want=%d err=%v", n, unread, err)
		}
		// Stable ordering and deduplication before LIMIT/OFFSET, including multi-target matches.
		for i, r := range list {
			page, err := q.ListVisibleAnnouncements(ctx, dbgen.ListVisibleAnnouncementsParams{UserID: user, RowLimit: 1, RowOffset: int32(i)})
			if err != nil || len(page) != 1 || page[0].ID != r.ID {
				t.Fatalf("pagination %d: %v", i, err)
			}
		}
	}
	svc := SportAssignmentService{Pool: pool}
	correct := func(user uuid.UUID, kind SportAssignmentKind, codes ...string) {
		t.Helper()
		if err := svc.Replace(ctx, SportAssignmentCorrection{ActorID: coach, MemberID: user, Kind: kind, Codes: codes, Reason: "Audience regression correction"}); err != nil {
			t.Fatal(err)
		}
	}
	membershipSnapshot := func() string {
		t.Helper()
		var value string
		if err := pool.QueryRow(ctx, `SELECT jsonb_build_object('membership',(SELECT to_jsonb(m) FROM user_memberships m WHERE id=$1),'legacy',(SELECT jsonb_agg(to_jsonb(mm) ORDER BY modality_id) FROM membership_modalities mm WHERE membership_id=$1))::text`, membership).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	originalMembership := membershipSnapshot()
	// A stale K1 membership link alone confers no audience eligibility.
	assert(athlete, mixed)
	assert(guardian, mixed, guardedMixed, guardianOnly)
	correct(athlete, SportingModalityAssignment, "CANOEING", "SUP", "KAYAK_POLO")
	for _, c := range crafts {
		correct(athlete, CanoeCraftAssignment, c)
		assert(athlete, mixed, targets[c], targets["SUP"])
	}
	correct(athlete, CanoeCraftAssignment, crafts...)
	want := []uuid.UUID{mixed, targets["SUP"]}
	for _, c := range crafts {
		want = append(want, targets[c])
	}
	assert(athlete, want...)
	// The guardian's own K1 cannot satisfy the guarded target on a different child.
	correct(athlete, CanoeCraftAssignment)
	exec(`INSERT INTO person_sporting_modalities(user_id,modality_code) VALUES($1,'CANOEING')`, guardian)
	exec(`INSERT INTO person_canoe_craft_classes(user_id,craft_code) VALUES($1,'K1')`, guardian)
	assert(guardian, mixed, guardedMixed, guardianOnly)
	correct(child, SportingModalityAssignment, "CANOEING")
	correct(child, CanoeCraftAssignment, "K1")
	assert(guardian, mixed, guardedMixed, guardianOnly, guarded, targets["K1"])
	// A revoked sporting child cannot borrow authority from the other verified child.
	exec(`UPDATE guardian_authority_relationships SET state='SUSPENDED',version=version+1 WHERE subject_user_id=$1`, child)
	assert(guardian, guardianOnly)
	exec(`UPDATE guardian_authority_relationships SET state='VERIFIED',version=version+1 WHERE subject_user_id=$1`, child)
	exec(`UPDATE users SET is_active=false WHERE id=$1`, child)
	assert(guardian, guardianOnly)
	exec(`UPDATE users SET is_active=true WHERE id=$1`, child)
	// Independent persisted no/future/ended participation fixtures; do not bypass dated guards.
	for _, state := range []string{"none", "future", "ended"} {
		user := uuid.New()
		exec(`INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Participation fixture',$2,'hash','1990-01-01')`, user, user.String()+"@example.test")
		switch state {
		case "future":
			exec(`INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on) VALUES($1,$2,$3,CURRENT_DATE+1)`, user, season, programme)
		case "ended":
			exec(`INSERT INTO user_memberships(user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,CURRENT_DATE-2,CURRENT_DATE-1)`, user, season, programme)
		}
		baseline[user], err = q.CountUnreadVisibleAnnouncements(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		for _, change := range []SportAssignmentCorrection{{Kind: SportingModalityAssignment, Codes: []string{"CANOEING", "SUP"}}, {Kind: CanoeCraftAssignment, Codes: crafts}} {
			change.ActorID, change.MemberID, change.Reason = admin, user, "Synthetic participation boundary"
			if err := svc.Replace(ctx, change); err != nil {
				t.Fatal(err)
			}
		}
		assert(user)
	}
	assert(athlete, mixed, targets["SUP"])
	exec(`UPDATE users SET is_active=false WHERE id=$1`, athlete)
	assert(athlete)
	exec(`UPDATE users SET is_active=true WHERE id=$1`, athlete)
	// Deliver/read state survives loss and restoration of current sport eligibility.
	if err := q.RecordAnnouncementDelivery(ctx, dbgen.RecordAnnouncementDeliveryParams{AnnouncementID: targets["SUP"], UserID: athlete}); err != nil {
		t.Fatal(err)
	}
	if err := q.MarkAnnouncementRead(ctx, dbgen.MarkAnnouncementReadParams{AnnouncementID: targets["SUP"], UserID: athlete}); err != nil {
		t.Fatal(err)
	}
	read[targets["SUP"]] = true
	var before string
	snapshot := func() string {
		t.Helper()
		var s string
		if err := pool.QueryRow(ctx, `SELECT jsonb_build_object('announcements',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM announcements a WHERE id=ANY($1)),'audit',(SELECT jsonb_agg(to_jsonb(e) ORDER BY id) FROM announcement_audit_events e WHERE announcement_id=ANY($1)),'targets',(SELECT jsonb_agg(to_jsonb(t) ORDER BY target_type,target_id) FROM announcement_targets t WHERE announcement_id=ANY($1)),'delivery',(SELECT to_jsonb(d) FROM announcement_deliveries d WHERE announcement_id=$2 AND user_id=$3))::text`, mapKeysAnnouncement(all), targets["SUP"], athlete).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	before = snapshot()
	correct(athlete, SportingModalityAssignment, "KAYAK_POLO")
	assert(athlete, mixed)
	correct(athlete, SportingModalityAssignment, "SUP")
	assert(athlete, mixed, targets["SUP"])
	if membershipSnapshot() != originalMembership {
		t.Fatal("sport correction rewrote participation or legacy modality links")
	}
	if snapshot() != before {
		t.Fatal("correction rewrote announcement identity, targets or receipt")
	}
	if _, err := q.ExpireAnnouncement(ctx, dbgen.ExpireAnnouncementParams{ID: targets["SUP"], ActorUserID: &admin}); err != nil {
		t.Fatal(err)
	}
	assert(athlete, mixed)
	if _, err := q.ExpireAnnouncement(ctx, dbgen.ExpireAnnouncementParams{ID: mixed, ActorUserID: &admin}); err != nil {
		t.Fatal(err)
	}
	assert(athlete)
	exec(`UPDATE users SET is_active=false WHERE id=$1`, athlete)
	assert(athlete)
}

func mapKeysAnnouncement(values map[uuid.UUID]bool) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(values))
	for id := range values {
		ids = append(ids, id)
	}
	return ids
}
