// Package trainingcompat describes the immutable reader/writer generation of
// this binary. It supplies evidence to an external controller, not authority to
// activate database capabilities or an assertion that a restore never happened.
package trainingcompat

import "github.com/cfcoimbra/mycfc/internal/releasecontract"

const ReaderGeneration int32 = 3
const WriterGeneration int32 = 2

type Capabilities struct {
	Protocol         int      `json:"protocol"`
	Version          string   `json:"version"`
	Candidate        string   `json:"candidate"`
	ReaderGeneration int32    `json:"reader_generation"`
	WriterGeneration int32    `json:"writer_generation"`
	SnapshotRead     []int    `json:"snapshot_read"`
	SchemaRead       []string `json:"schema_read"`
	Write            []string `json:"write"`
}

func Compiled() Capabilities {
	return Capabilities{Protocol: 1, Version: releasecontract.Version, Candidate: releasecontract.Candidate, ReaderGeneration: ReaderGeneration, WriterGeneration: WriterGeneration, SnapshotRead: []int{1, 2}, SchemaRead: []string{"schedule-c1", "schedule-p1"}, Write: []string{"INTERVAL", "START_ONLY", "DAY", "BAND", "snapshot-v1", "snapshot-v2"}}
}
