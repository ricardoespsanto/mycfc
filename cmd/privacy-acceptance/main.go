// privacy-acceptance owns synthetic acceptance and its isolated operator login.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/cfcoimbra/mycfc/internal/privacyrequests"
	"github.com/jackc/pgx/v5"
)

const maxCredentialFile = 16384

func main() {
	signalContext, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(signalContext, 45*time.Minute)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Getenv, os.Geteuid(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "privacy_acceptance_failed")
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, uid int, out io.Writer) error {
	if uid != 0 || len(args) != 1 {
		return errors.New("acceptance operator command rejected")
	}
	if privacyrequests.AcceptanceMode(args[0]) {
		return runAcceptance(ctx, args[0], getenv, out)
	}
	switch args[0] {
	case "provision", "rotate", "revoke":
	default:
		return errors.New("acceptance operator mode rejected")
	}
	expected := getenv("PRIVACY_ACCEPTANCE_EXPECTED_DATABASE")
	adminRaw, err := readProtectedFile(getenv("PRIVACY_ACCEPTANCE_ADMIN_DATABASE_URL_FILE"), 0)
	if err != nil {
		return err
	}
	admin, err := pgx.ParseConfig(strings.TrimSpace(string(adminRaw)))
	if err != nil || admin.Database != expected || expected == "" {
		return errors.New("acceptance administrator binding rejected")
	}
	password := ""
	if args[0] != "revoke" {
		raw, e := readProtectedFile(getenv("PRIVACY_ACCEPTANCE_DATABASE_URL_FILE"), 0)
		if e != nil {
			return e
		}
		operator, e := pgx.ParseConfig(strings.TrimSpace(string(raw)))
		if e != nil || operator.Database != expected || operator.User != db.PrivacyAcceptanceRole || operator.Host != admin.Host || operator.Port != admin.Port || len(operator.Password) < 32 {
			return errors.New("acceptance operator binding rejected")
		}
		password = operator.Password
	}
	conn, err := pgx.ConnectConfig(ctx, admin)
	if err != nil {
		return errors.New("acceptance administrator unavailable")
	}
	defer conn.Close(ctx)
	if err = db.ConfigurePrivacyAcceptanceRole(ctx, conn, expected, password, args[0] == "revoke"); err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "event=privacy_acceptance_credential operation=%s outcome=complete\n", args[0])
	return err
}

// Open and inspect the same descriptor; a symlink swap cannot replace the
// verified file. Parent directories must be root-owned operator directories.
func readProtectedFile(path string, owner uint32) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, errors.New("acceptance credential file unavailable")
	}
	file := os.NewFile(uintptr(fd), "protected-credential")
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() <= 0 || info.Size() > maxCredentialFile {
		return nil, errors.New("acceptance credential file rejected")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != owner || stat.Gid != owner {
		return nil, errors.New("acceptance credential owner rejected")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxCredentialFile+1))
	if err != nil || len(data) > maxCredentialFile {
		return nil, errors.New("acceptance credential read rejected")
	}
	return data, nil
}
