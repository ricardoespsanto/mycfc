package trainingcompat

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompatibilityBinaryDeclaresOnlyExactV1WritesAndDualReaders(t *testing.T) {
	c := Compiled()
	raw, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	for _, expected := range []string{`"reader_generation":2`, `"writer_generation":1`, `"snapshot_read":[1,2]`, `"schema_read":["schedule-c1","schedule-p1"]`, `"write":["INTERVAL","snapshot-v1"]`} {
		if !strings.Contains(string(raw), expected) {
			t.Fatalf("missing %s: %s", expected, raw)
		}
	}
	c.SnapshotRead[0] = 99
	if Compiled().SnapshotRead[0] != 1 {
		t.Fatal("capabilities mutated across calls")
	}
}
