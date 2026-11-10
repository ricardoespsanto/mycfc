package trainingcompat

import (
	"encoding/json"
	"strings"
	"testing"
)

func admissionFixture() ([]byte, Capabilities, RuntimeState) {
	c := Compiled()
	c.Version = "v2.0.9"
	c.Candidate = strings.Repeat("a", 40)
	state := RuntimeState{DatabaseIncarnation: "11111111-1111-4111-8111-111111111111", SchemaContract: "schedule-c1", StateEpoch: 3, RequiredReader: 1}
	p := Admission{Contract: AdmissionContract, Admission: "admitted", RestoreGeneration: 4, ReconciledRestoreGeneration: 4, DatabaseIncarnation: state.DatabaseIncarnation, MinimumStateEpoch: 2, MinimumReader: 1, Images: []AdmissionImage{{Digest: "sha256:" + strings.Repeat("b", 64), Capabilities: c}}}
	raw, _ := json.Marshal(p)
	return raw, c, state
}
func TestAdmissionRequiresExternalReconciliationAsWellAsCurrentDatabaseFacts(t *testing.T) {
	raw, c, state := admissionFixture()
	p, e := ParseAdmission(raw)
	if e != nil {
		t.Fatal(e)
	}
	if e = p.Verify(state, c); e != nil {
		t.Fatal(e)
	}
	p.ReconciledRestoreGeneration = 3
	if p.Verify(state, c) == nil {
		t.Fatal("copied incarnation/epoch admitted unreconciled restore")
	}
	p.ReconciledRestoreGeneration = 4
	p.Admission = "blocked"
	if p.Verify(state, c) == nil {
		t.Fatal("blocked operator fence bypassed")
	}
	p.Admission = "admitted"
	state.StateEpoch = 1
	if p.Verify(state, c) == nil {
		t.Fatal("rewound below external checkpoint admitted")
	}
}
func TestAdmissionRejectsUnknownDuplicateAndMalformedFacts(t *testing.T) {
	raw, _, _ := admissionFixture()
	valid := string(raw)
	for name, bad := range map[string]string{
		"duplicate":          strings.Replace(valid, `"restore_generation":4`, `"restore_generation":4,"restore_generation":4`, 1),
		"unknown":            strings.Replace(valid, `"contract":`, `"unexpected":true,"contract":`, 1),
		"missing":            strings.Replace(valid, `"minimum_state_epoch":2,`, "", 1),
		"null integer":       strings.Replace(valid, `"minimum_state_epoch":2`, `"minimum_state_epoch":null`, 1),
		"float":              strings.Replace(valid, `"restore_generation":4`, `"restore_generation":4.0`, 1),
		"boolean":            strings.Replace(valid, `"restore_generation":4`, `"restore_generation":true`, 1),
		"overflow":           strings.Replace(valid, `"restore_generation":4`, `"restore_generation":9223372036854775808`, 1),
		"bad floor pair":     strings.Replace(valid, `"floor_commit_id":null`, `"floor_commit_id":"22222222-2222-4222-8222-222222222222"`, 1),
		"reconcile future":   strings.Replace(valid, `"reconciled_restore_generation":4`, `"reconciled_restore_generation":5`, 1),
		"unknown capability": strings.Replace(valid, `"snapshot-v1"`, `"snapshot-v99"`, 1),
		"duplicate array":    strings.Replace(valid, `"snapshot_read":[1,2]`, `"snapshot_read":[1,1]`, 1),
		"duplicate nested":   strings.Replace(valid, `"protocol":1`, `"protocol":1,"protocol":1`, 1),
		"trailing":           valid + ` {}`,
		"invalid utf8":       valid + string([]byte{0xff}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, e := ParseAdmission([]byte(bad)); e == nil {
				t.Fatalf("accepted %s", bad)
			}
		})
	}
}
func TestAdmissionBindsCompiledStampFloorAndReaderGeneration(t *testing.T) {
	raw, c, state := admissionFixture()
	p, _ := ParseAdmission(raw)
	different := c
	different.Candidate = strings.Repeat("c", 40)
	if p.Verify(state, different) == nil {
		t.Fatal("different binary admitted")
	}
	state.RequiredReader = 3
	if p.Verify(state, c) == nil {
		t.Fatal("later reader floor admitted C2")
	}
	state.RequiredReader = 1
	id := "22222222-2222-4222-8222-222222222222"
	state.FloorCommitID = &id
	if p.Verify(state, c) == nil {
		t.Fatal("different DB floor admitted")
	}
}
func TestAdmissionFileRejectsWritableLocalAncestorAndSymlink(t *testing.T) {
	if _, e := ReadAdmission(t.TempDir() + "/admission.json"); e == nil {
		t.Fatal("untrusted writable directory admitted")
	}
	for _, path := range []string{"relative.json", "/run/../run/mycfc-admission/admission.json", "/proc/self/fd/0"} {
		if _, e := ReadAdmission(path); e == nil {
			t.Fatalf("accepted %s", path)
		}
	}
}
