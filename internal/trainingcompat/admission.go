package trainingcompat

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/google/uuid"
)

const AdmissionContract = "mycfc/training-schedule-admission/v1"
const DefaultAdmissionFile = "/run/mycfc-admission/admission.json"

var ErrAdmission = errors.New("training schedule external admission unavailable")
var stampVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)
var stampCandidate = regexp.MustCompile(`^[0-9a-f]{40}$`)
var digestHex = regexp.MustCompile(`^[0-9a-f]{64}$`)

type AdmissionImage struct {
	Digest       string       `json:"image_digest"`
	Capabilities Capabilities `json:"capabilities"`
}
type Admission struct {
	Contract                    string           `json:"contract"`
	Admission                   string           `json:"admission"`
	RestoreGeneration           int64            `json:"restore_generation"`
	ReconciledRestoreGeneration int64            `json:"reconciled_restore_generation"`
	DatabaseIncarnation         string           `json:"database_incarnation"`
	FloorCommitID               *string          `json:"floor_commit_id"`
	FloorManifestSHA256         *string          `json:"floor_manifest_sha256"`
	MinimumStateEpoch           int64            `json:"minimum_state_epoch"`
	MinimumReader               int32            `json:"minimum_reader"`
	Images                      []AdmissionImage `json:"images"`
}
type RuntimeState struct {
	DatabaseIncarnation, SchemaContract string
	StateEpoch                          int64
	RequiredReader                      int32
	FloorCommitID, FloorManifestSHA256  *string
}

func ParseAdmission(raw []byte) (Admission, error) {
	var p Admission
	if len(raw) == 0 || len(raw) > 65536 || !utf8.Valid(raw) {
		return p, ErrAdmission
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if e := uniqueJSON(d, 0); e != nil {
		return p, e
	}
	if _, e := d.Token(); e != io.EOF {
		return p, ErrAdmission
	}
	var tree map[string]json.RawMessage
	if json.Unmarshal(raw, &tree) != nil || !exactKeys(tree, "contract", "admission", "restore_generation", "reconciled_restore_generation", "database_incarnation", "floor_commit_id", "floor_manifest_sha256", "minimum_state_epoch", "minimum_reader", "images") {
		return p, ErrAdmission
	}
	for key, value := range tree {
		if key != "floor_commit_id" && key != "floor_manifest_sha256" && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return p, ErrAdmission
		}
	}
	var images []map[string]json.RawMessage
	if json.Unmarshal(tree["images"], &images) != nil || len(images) == 0 || len(images) > 8 {
		return p, ErrAdmission
	}
	for _, im := range images {
		if !exactKeys(im, "image_digest", "capabilities") {
			return p, ErrAdmission
		}
		var c map[string]json.RawMessage
		if json.Unmarshal(im["capabilities"], &c) != nil || !exactKeys(c, "protocol", "version", "candidate", "reader_generation", "writer_generation", "snapshot_read", "schema_read", "write") {
			return p, ErrAdmission
		}
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil {
		return p, ErrAdmission
	}
	if p.Contract != AdmissionContract || (p.Admission != "admitted" && p.Admission != "blocked") || p.RestoreGeneration < 1 || p.ReconciledRestoreGeneration < 0 || p.ReconciledRestoreGeneration > p.RestoreGeneration || p.MinimumStateEpoch < 0 || p.MinimumReader < 1 || !canonicalUUID(p.DatabaseIncarnation) {
		return p, ErrAdmission
	}
	if (p.FloorCommitID == nil) != (p.FloorManifestSHA256 == nil) {
		return p, ErrAdmission
	}
	if p.FloorCommitID != nil && (!canonicalUUID(*p.FloorCommitID) || !digestHex.MatchString(*p.FloorManifestSHA256) || p.MinimumReader < 2) {
		return p, ErrAdmission
	}
	seenDigest, seenIdentity := map[string]bool{}, map[string]bool{}
	for _, im := range p.Images {
		c := im.Capabilities
		if !strings.HasPrefix(im.Digest, "sha256:") || !digestHex.MatchString(strings.TrimPrefix(im.Digest, "sha256:")) || seenDigest[im.Digest] || !validCapabilities(c) {
			return p, ErrAdmission
		}
		identity := c.Version + "/" + c.Candidate
		if seenIdentity[identity] {
			return p, ErrAdmission
		}
		seenIdentity[identity] = true
		seenDigest[im.Digest] = true
	}
	return p, nil
}
func validCapabilities(c Capabilities) bool {
	return c.Protocol == 1 && stampVersion.MatchString(c.Version) && stampCandidate.MatchString(c.Candidate) && c.ReaderGeneration > 0 && c.WriterGeneration > 0 && orderedInts(c.SnapshotRead, []int{1, 2}) && orderedStrings(c.SchemaRead, []string{"schedule-c1", "schedule-p1"}) && orderedStrings(c.Write, []string{"INTERVAL", "START_ONLY", "DAY", "BAND", "snapshot-v1", "snapshot-v2"})
}
func orderedInts(v, allowed []int) bool {
	if len(v) == 0 || len(v) > len(allowed) {
		return false
	}
	last := -1
	for _, x := range v {
		i := slices.Index(allowed, x)
		if i <= last {
			return false
		}
		last = i
	}
	return true
}
func orderedStrings(v, allowed []string) bool {
	if len(v) == 0 || len(v) > len(allowed) {
		return false
	}
	last := -1
	for _, x := range v {
		i := slices.Index(allowed, x)
		if i <= last {
			return false
		}
		last = i
	}
	return true
}
func canonicalUUID(s string) bool {
	v, e := uuid.Parse(s)
	return e == nil && v != uuid.Nil && v.String() == s
}
func exactKeys(m map[string]json.RawMessage, keys ...string) bool {
	if len(m) != len(keys) {
		return false
	}
	for _, k := range keys {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}
func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > 16 {
		return ErrAdmission
	}
	t, e := d.Token()
	if e != nil {
		return ErrAdmission
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, e := d.Token()
			key, ok := k.(string)
			if e != nil || !ok || seen[key] {
				return ErrAdmission
			}
			seen[key] = true
			if e = uniqueJSON(d, depth+1); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e = uniqueJSON(d, depth+1); e != nil {
				return e
			}
		}
	default:
		return ErrAdmission
	}
	if _, e = d.Token(); e != nil {
		return ErrAdmission
	}
	return nil
}

// The file is an external operator-fenced admission decision. Matching copied
// database facts alone does not demonstrate that an unknown restore never ran.
func (p Admission) Verify(state RuntimeState, compiled Capabilities) error {
	if p.Admission != "admitted" || p.RestoreGeneration != p.ReconciledRestoreGeneration || p.DatabaseIncarnation != state.DatabaseIncarnation || state.StateEpoch < p.MinimumStateEpoch || state.RequiredReader < p.MinimumReader || state.RequiredReader > compiled.ReaderGeneration || !slices.Contains(compiled.SchemaRead, state.SchemaContract) || !equalStringPointer(p.FloorCommitID, state.FloorCommitID) || !equalStringPointer(p.FloorManifestSHA256, state.FloorManifestSHA256) {
		return ErrAdmission
	}
	for _, im := range p.Images {
		if reflect.DeepEqual(im.Capabilities, compiled) {
			return nil
		}
	}
	return ErrAdmission
}
func equalStringPointer(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// Open each path component relative to the previous descriptor with NOFOLLOW.
// This avoids a check/open race and rejects writable ancestors and symlinks.
func ReadAdmission(path string) (Admission, error) {
	var empty Admission
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return empty, ErrAdmission
	}
	components := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(components) < 2 {
		return empty, ErrAdmission
	}
	fd, e := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if e != nil {
		return empty, ErrAdmission
	}
	defer func() { _ = syscall.Close(fd) }()
	for i, part := range components {
		if part == "" || part == "." || part == ".." {
			return empty, ErrAdmission
		}
		flags := syscall.O_RDONLY | syscall.O_NOFOLLOW | syscall.O_CLOEXEC | syscall.O_NONBLOCK
		if i < len(components)-1 {
			flags |= syscall.O_DIRECTORY
		}
		next, e := syscall.Openat(fd, part, flags, 0)
		if e != nil {
			return empty, ErrAdmission
		}
		_ = syscall.Close(fd)
		fd = next
		var stat syscall.Stat_t
		if syscall.Fstat(fd, &stat) != nil || stat.Uid != 0 {
			return empty, ErrAdmission
		}
		if i < len(components)-1 {
			if stat.Mode&0022 != 0 {
				return empty, ErrAdmission
			}
			if i == len(components)-2 && stat.Mode&0777 != 0755 {
				return empty, ErrAdmission
			}
		} else {
			if stat.Mode&syscall.S_IFMT != syscall.S_IFREG || stat.Mode&07777 != 0444 || stat.Size > 65536 {
				return empty, ErrAdmission
			}
		}
	}
	file := os.NewFile(uintptr(fd), "admission")
	if file == nil {
		return empty, ErrAdmission
	}
	fd = -1
	defer file.Close()
	raw, e := io.ReadAll(io.LimitReader(file, 65537))
	if e != nil {
		return empty, ErrAdmission
	}
	return ParseAdmission(raw)
}
