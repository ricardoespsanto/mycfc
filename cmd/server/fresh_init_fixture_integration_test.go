//go:build integration

package main

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// isolatedFreshInitURL never touches the integration suite's shared DB: a
// stamped fresh release requires the fixed name mycfc in its own PG cluster.
func isolatedFreshInitURL(t *testing.T) string {
	t.Helper()
	name := "mycfc-fresh-ci-" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	password := strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := exec.Command("docker", "run", "-d", "--rm", "--name", name, "-P",
		"-e", "POSTGRES_DB=mycfc", "-e", "POSTGRES_PASSWORD="+password, "postgres:16.9").Run(); err != nil {
		t.Fatalf("start isolated fresh PostgreSQL: %v", err)
	}
	t.Cleanup(func() {
		if err := exec.Command("docker", "stop", name).Run(); err != nil {
			t.Errorf("stop isolated fresh PostgreSQL: %v", err)
		}
	})
	portOutput, err := exec.Command("docker", "port", name, "5432/tcp").Output()
	if err != nil {
		t.Fatalf("resolve isolated fresh PostgreSQL port: %v", err)
	}
	line := strings.SplitN(strings.TrimSpace(string(portOutput)), "\n", 2)[0]
	index := strings.LastIndex(line, ":")
	if index < 0 || index == len(line)-1 {
		t.Fatal("isolated fresh PostgreSQL port unavailable")
	}
	url := "postgres://postgres:" + password + "@127.0.0.1:" + line[index+1:] + "/mycfc?sslmode=disable"
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := pgx.Connect(t.Context(), url)
		if err == nil {
			_ = conn.Close(t.Context())
			t.Log("using isolated fresh PostgreSQL container with fixed database name")
			return url
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("isolated fresh PostgreSQL did not become ready")
	return ""
}
