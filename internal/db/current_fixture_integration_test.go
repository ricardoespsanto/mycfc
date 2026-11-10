//go:build integration

package db

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// Current fixture preparation is explicit and local to NEW synthetic databases.
// It does not convert application records or weaken production admission.
func recipientPrepareCurrentReader(t *testing.T, f recipientSchemaFixture) {
	t.Helper()
	s, err := TrainingScheduleStatus(t.Context(), f.low)
	if err != nil {
		t.Fatal(err)
	}
	if s.RequiredReader < 5 {
		var epoch int64
		if err = f.low.QueryRow(t.Context(), `SELECT training_schedule_control.commit_floor($1,$2,$3,$4,5)`, s.DatabaseIncarnation, s.StateEpoch, uuid.New(), strings.Repeat("d", 64)).Scan(&epoch); err != nil {
			t.Fatal(err)
		}
		s, err = TrainingScheduleStatus(t.Context(), f.low)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !s.PartialV2Enabled {
		var epoch int64
		if err = f.low.QueryRow(t.Context(), `SELECT training_schedule_control.set_enabled($1,$2,true)`, s.DatabaseIncarnation, s.StateEpoch).Scan(&epoch); err != nil {
			t.Fatal(err)
		}
	}
}
func recipientPlaceFixtureMember(t *testing.T, f recipientSchemaFixture, actor, member, group uuid.UUID) uuid.UUID {
	t.Helper()
	recipientPrepareCurrentReader(t, f)
	var user, programme, season uuid.UUID
	var day pgtype.Date
	if err := f.low.QueryRow(t.Context(), `SELECT user_id,programme_id,season_id,GREATEST(starts_on,(clock_timestamp() AT TIME ZONE 'Europe/Lisbon')::date) FROM user_memberships WHERE id=$1`, member).Scan(&user, &programme, &season, &day); err != nil {
		t.Fatal(err)
	}
	tx, err := f.low.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	// Owner fixture constructs a coherent initial fact, including historical cases,
	// without disabling triggers or invoking a backdated web command.
	change, placement := uuid.New(), uuid.New()
	recipientSchemaExec(t, tx, `INSERT INTO training_placement_heads(subject_user_id,programme_id,revision) VALUES($1,$2,1) ON CONFLICT(subject_user_id,programme_id) DO UPDATE SET revision=training_placement_heads.revision+1`, user, programme)
	recipientSchemaExec(t, tx, `INSERT INTO training_group_placements(id,subject_user_id,programme_id,group_id,starts_on,change_id) VALUES($1,$2,$3,$4,$5,$6)`, placement, user, programme, group, day, change)
	recipientSchemaExec(t, tx, `INSERT INTO training_placement_changes(id,subject_user_id,programme_id,season_id,kind,destination_placement_id,effective_on,actor_user_id,canonical_command,head_revision) SELECT $1,$2,$3,$4,'JOIN',$5,$6,$7,jsonb_build_object('synthetic_initial_fixture',true),revision FROM training_placement_heads WHERE subject_user_id=$2 AND programme_id=$3`, change, user, programme, season, placement, day, actor)
	if err = tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	return placement
}
func recipientCurrentPublicationFixture(t *testing.T) (recipientSchemaFixture, recipientHistory) {
	t.Helper()
	f := newRecipientSchemaFixture(t)
	if err := ApplyBaselineAndHarden(t.Context(), f.low, "mycfc", f.roles); err != nil {
		t.Fatal(err)
	}
	recipientPrepareCurrentReader(t, f)
	web := f.web(t)
	h := recipientHistory{actor: uuid.New(), subject: uuid.New(), membership: uuid.New(), group: uuid.New(), plan: uuid.New(), session: uuid.New(), publication: uuid.New(), prescription: uuid.New()}
	season := uuid.New()
	for _, id := range []uuid.UUID{h.actor, h.subject} {
		recipientSchemaExec(t, web, `INSERT INTO users(id,name,email,password_hash,date_of_birth) VALUES($1,'Synthetic current publication subject',$2,'hash','1980-01-01')`, id, id.String()+"@example.test")
	}
	recipientSchemaExec(t, web, `INSERT INTO user_platform_roles(user_id,role_id) SELECT $1,id FROM platform_roles WHERE code='ADMIN'`, h.actor)
	recipientSchemaExec(t, web, `INSERT INTO seasons(id,code,name,starts_on,ends_on) VALUES($1,'SYN_CURRENT','Synthetic current season',CURRENT_DATE-365,CURRENT_DATE+400)`, season)
	var programme uuid.UUID
	if err := web.QueryRow(t.Context(), `SELECT id FROM programmes WHERE code='Leisure'`).Scan(&programme); err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, web, `INSERT INTO user_memberships(id,user_id,season_id,programme_id,starts_on,ends_on) VALUES($1,$2,$3,$4,CURRENT_DATE,CURRENT_DATE+300)`, h.membership, h.subject, season, programme)
	recipientSchemaExec(t, web, `INSERT INTO training_groups(id,name,programme_id,created_by_id) VALUES($1,'Synthetic current group',$2,$3)`, h.group, programme, h.actor)
	h.placement = recipientPlaceFixtureMember(t, f, h.actor, h.membership, h.group)
	recipientSchemaExec(t, web, `INSERT INTO training_plans(id,title,programme_id,training_group_id,season_id,week_start,created_by_id) VALUES($1,'Synthetic current publication week',$2,$3,$4,date_trunc('week',CURRENT_DATE)::date+7,$5)`, h.plan, programme, h.group, season, h.actor)
	recipientSchemaExec(t, web, `INSERT INTO training_sessions(id,plan_id,title,starts_at,ends_at,created_by_id) SELECT $1,id,'Synthetic exact current session',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '10 hours',(week_start::timestamp AT TIME ZONE 'Europe/Lisbon')+interval '11 hours',$3 FROM training_plans WHERE id=$2`, h.session, h.plan, h.actor)
	if err := web.QueryRow(t.Context(), `SELECT updated_at FROM training_plans WHERE id=$1`, h.plan).Scan(&h.source); err != nil {
		t.Fatal(err)
	}
	recipientSchemaExec(t, web, `INSERT INTO training_plan_publications(id,plan_id,revision,source_updated_at,change_summary,published_by_id) VALUES($1,$2,1,$3,'Synthetic first current publication',$4)`, h.publication, h.plan, h.source, h.actor)
	raw := `{"schema_version":1,"session":{"EntryKind":"TRAINING"}}`
	sum := sha256.Sum256([]byte(raw))
	recipientSchemaExec(t, web, `INSERT INTO training_prescriptions(id,publication_id,session_id,membership_id,athlete_user_id,placement_id,snapshot,snapshot_sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, h.prescription, h.publication, h.session, h.membership, h.subject, h.placement, raw, hex.EncodeToString(sum[:]))
	recipientSchemaExec(t, web, `INSERT INTO training_session_outcomes(session_id,user_id,prescription_id,status) VALUES($1,$2,$3,'COMPLETED')`, h.session, h.subject, h.prescription)
	h.before = recipientHistoryBytes(t, f.low, h)
	return f, h
}
