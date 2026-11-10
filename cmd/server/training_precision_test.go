package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/releasecontract"
)

func TestNormalReleaseContractRequiresCompiledIdentityAndExactFinalInventory(t *testing.T) {
	version, candidate := releasecontract.Version, releasecontract.Candidate
	t.Cleanup(func() { releasecontract.Version, releasecontract.Candidate = version, candidate })
	releasecontract.Version, releasecontract.Candidate = "", ""
	if e := runServerCommand(t.Context(), []string{"normal-release-contract"}); e == nil {
		t.Fatal("unstamped normal contract emitted")
	}
	releasecontract.Version, releasecontract.Candidate = "v99.0.0", strings.Repeat("a", 40)
	previous := os.Stdout
	read, write, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	defer read.Close()
	os.Stdout = write
	t.Cleanup(func() { os.Stdout = previous })
	if e = runServerCommand(t.Context(), []string{"normal-release-contract"}); e != nil {
		_ = write.Close()
		t.Fatal(e)
	}
	_ = write.Close()
	os.Stdout = previous
	raw, e := io.ReadAll(read)
	if e != nil {
		t.Fatal(e)
	}
	var object map[string]string
	if e = json.Unmarshal(raw, &object); e != nil {
		t.Fatal(e)
	}
	if len(object) != 6 || object["contract"] != "mycfc/normal-release/v1" || object["version"] != "v99.0.0" || object["candidate"] != strings.Repeat("a", 40) || object["database"] != "mycfc" || object["parent_digest"] != releasecontract.NormalParentDigest || object["final_digest"] != db.EmbeddedMigrationDigest() {
		t.Fatalf("wrong normal contract %s", raw)
	}
	if _, ok := object["baseline_digest"]; ok {
		t.Fatal("retired reset pin became normal parent")
	}
}
