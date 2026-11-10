package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"syscall"

	"github.com/cfcoimbra/mycfc/internal/db"
	"github.com/jackc/pgx/v5"
)

type localRecipientManifest struct {
	Contract       string                     `json:"contract"`
	ProjectPath    string                     `json:"project_path"`
	SystemID       string                     `json:"system_identifier"`
	Database       string                     `json:"database"`
	DatabaseOID    uint32                     `json:"database_oid"`
	EndpointSHA256 string                     `json:"endpoint_sha256"`
	MigrationRole  string                     `json:"migration_role"`
	PublisherRole  string                     `json:"publisher_role"`
	PublisherOID   *uint32                    `json:"publisher_oid"`
	OriginalLogin  *bool                      `json:"original_login"`
	Phase          string                     `json:"phase"`
	Intent         *localRecipientFloorIntent `json:"intent"`
}
type localRecipientFloorIntent struct {
	Incarnation    string `json:"incarnation"`
	ExpectedEpoch  int64  `json:"expected_epoch"`
	Commit         string `json:"commit"`
	MinimumReader  int32  `json:"minimum_reader"`
	ManifestSHA256 string `json:"manifest_sha256"`
}
type localRecipientManifestFile struct {
	path  string
	lock  *os.File
	Value localRecipientManifest
}

func (f *localRecipientManifestFile) Close() {
	_ = syscall.Flock(int(f.lock.Fd()), syscall.LOCK_UN)
	_ = f.lock.Close()
}
func localOwnedPath(path string, mode os.FileMode, directory bool) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != mode || info.IsDir() != directory {
		return errors.New("local fixture path ownership or mode rejected")
	}
	return nil
}
func localManifestBinding(ctx context.Context, bootstrap *pgx.Conn, roles db.RoleCredentials) (localRecipientManifest, error) {
	var m localRecipientManifest
	if !strictUnstampedLocalConnection(bootstrap.Config()) || os.Getenv("DATABASE_URL") == "" {
		return m, errors.New("local fixture requires explicit owned loopback URL and wholly unstamped local/test configuration")
	}
	if err := requireRecipientBootstrapIdentity(ctx, bootstrap, os.Getenv("POSTGRES_USER")); err != nil {
		return m, err
	}
	cwd, cwdErr := os.Getwd()
	if cwdErr != nil {
		return m, cwdErr
	}
	m.ProjectPath = cwd
	m.Contract = "mycfc/local-recipient-floor/v1"
	m.Database = bootstrap.Config().Database
	m.MigrationRole = roles.MigrationUsername
	m.PublisherRole = roles.AppUsername
	if err := bootstrap.QueryRow(ctx, `SELECT control.system_identifier::pg_catalog.text,database.oid FROM pg_catalog.pg_control_system() control CROSS JOIN pg_catalog.pg_database database WHERE database.datname OPERATOR(pg_catalog.=) pg_catalog.current_database()`).Scan(&m.SystemID, &m.DatabaseOID); err != nil {
		return m, errors.New("local fixture identity inspection failed")
	}
	u, err := url.Parse(os.Getenv("DATABASE_URL"))
	if err != nil {
		return m, errors.New("local fixture endpoint rejected")
	}
	u.User = nil
	digest := sha256.Sum256([]byte(u.String()))
	m.EndpointSHA256 = hex.EncodeToString(digest[:])
	return m, nil
}
func openLocalRecipientManifest(binding localRecipientManifest, create bool) (*localRecipientManifestFile, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	// Only the ignored project temporary tree is used; no operator-owned host
	// admission directory or root release record is read or written here.
	tmp := filepath.Join(cwd, "tmp")
	if err = os.Mkdir(tmp, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	info, err := os.Lstat(tmp)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("local temporary directory rejected")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return nil, errors.New("local temporary directory owner rejected")
	}
	dir := filepath.Join(tmp, "local-recipient-floor")
	if err = os.Mkdir(dir, 0700); err != nil && !os.IsExist(err) {
		return nil, err
	}
	if err = localOwnedPath(dir, 0700, true); err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(fmt.Sprintf("%s/%d/%s/%s", binding.SystemID, binding.DatabaseOID, binding.Database, binding.PublisherRole)))
	path := filepath.Join(dir, hex.EncodeToString(key[:])+".json")
	lockPath := path + ".lock"
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	if err = localOwnedPath(lockPath, 0600, false); err != nil {
		lock.Close()
		return nil, err
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("local fixture operation already running")
	}
	f := &localRecipientManifestFile{path: path, lock: lock, Value: binding}
	if _, err = os.Lstat(path); os.IsNotExist(err) {
		if create {
			return f, nil
		}
		f.Close()
		return nil, errors.New("unrecorded populated local fixture: create a new owned fixture or deliberately reset with local tooling before bootstrap")
	}
	if err = localOwnedPath(path, 0600, false); err != nil {
		f.Close()
		return nil, err
	}
	sizeInfo, sizeErr := os.Lstat(path)
	if sizeErr != nil || sizeInfo.Size() > 8192 {
		f.Close()
		return nil, errors.New("local fixture manifest exceeds bound")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		f.Close()
		return nil, err
	}
	defer file.Close()
	dec := json.NewDecoder(io.LimitReader(file, 8193))
	dec.DisallowUnknownFields()
	var stored localRecipientManifest
	if err = dec.Decode(&stored); err != nil {
		f.Close()
		return nil, errors.New("local fixture manifest rejected")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		f.Close()
		return nil, errors.New("local fixture manifest trailing data")
	}
	if stored.ProjectPath != binding.ProjectPath || stored.Contract != binding.Contract || stored.SystemID != binding.SystemID || stored.Database != binding.Database || stored.DatabaseOID != binding.DatabaseOID || stored.EndpointSHA256 != binding.EndpointSHA256 || stored.MigrationRole != binding.MigrationRole || stored.PublisherRole != binding.PublisherRole || (stored.PublisherOID == nil) != (stored.OriginalLogin == nil) {
		f.Close()
		return nil, errors.New("local fixture manifest target changed")
	}
	switch stored.Phase {
	case "captured", "bootstrapped", "fenced", "floor_committed", "restored":
	default:
		f.Close()
		return nil, errors.New("local fixture phase rejected")
	}
	f.Value = stored
	return f, nil
}
func (f *localRecipientManifestFile) Save() error {
	b, err := json.Marshal(f.Value)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(f.path), ".local-floor-")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)
	if err = temp.Chmod(0600); err == nil {
		_, err = temp.Write(b)
	}
	if err == nil {
		err = temp.Sync()
	}
	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, f.path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(f.path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
func localPublisherIdentity(ctx context.Context, c *pgx.Conn, name string) (*uint32, *bool, error) {
	var oid *uint32
	var login *bool
	err := c.QueryRow(ctx, `SELECT (SELECT oid FROM pg_catalog.pg_roles WHERE rolname OPERATOR(pg_catalog.=) $1::pg_catalog.name),(SELECT rolcanlogin FROM pg_catalog.pg_roles WHERE rolname OPERATOR(pg_catalog.=) $1::pg_catalog.name)`, name).Scan(&oid, &login)
	return oid, login, err
}
func captureLocalRecipientProvenance(ctx context.Context, c *pgx.Conn, roles db.RoleCredentials) error {
	binding, err := localManifestBinding(ctx, c, roles)
	if err != nil {
		return err
	}
	f, err := openLocalRecipientManifest(binding, true)
	if err != nil {
		return err
	}
	defer f.Close()
	if f.Value.Phase != "" {
		return nil
	}
	f.Value.PublisherOID, f.Value.OriginalLogin, err = localPublisherIdentity(ctx, c, roles.AppUsername)
	if err != nil {
		return err
	}
	f.Value.Phase = "captured"
	return f.Save()
}
func completeLocalRecipientBootstrap(ctx context.Context, c *pgx.Conn) error {
	if !strictUnstampedLocalConnection(c.Config()) {
		return nil
	}
	roles := databaseRoleCredentialsFromEnvironment()
	if !databaseRoleIdentifiersComplete(roles) {
		return nil
	}
	binding, err := localManifestBinding(ctx, c, roles)
	if err != nil {
		return err
	}
	f, err := openLocalRecipientManifest(binding, false)
	if err != nil {
		return err
	}
	defer f.Close()
	if f.Value.Phase != "captured" {
		return nil
	}
	oid, login, err := localPublisherIdentity(ctx, c, roles.AppUsername)
	if err != nil {
		return err
	}
	if oid == nil || login == nil {
		return errors.New("local bootstrap publisher was not created")
	}
	if f.Value.PublisherOID != nil && *f.Value.PublisherOID != *oid {
		return errors.New("local bootstrap publisher OID changed")
	}
	if f.Value.PublisherOID == nil {
		f.Value.PublisherOID, f.Value.OriginalLogin = oid, login
	}
	f.Value.Phase = "bootstrapped"
	return f.Save()
}

func requireLocalRecipientProvenance(ctx context.Context, c *pgx.Conn, roles db.RoleCredentials) error {
	bootstrap, err := localBootstrapClone(ctx, c)
	if err != nil {
		return err
	}
	defer bootstrap.Close(ctx)
	binding, err := localManifestBinding(ctx, bootstrap, roles)
	if err != nil {
		return err
	}
	f, err := openLocalRecipientManifest(binding, false)
	if err != nil {
		return err
	}
	defer f.Close()
	if f.Value.Phase != "bootstrapped" && f.Value.Phase != "restored" && !(f.Value.Phase == "captured" && c.Config().User == os.Getenv("POSTGRES_USER")) {
		return errors.New("local fixture provenance is incomplete; inspect recorded phase")
	}
	return nil
}
func localBootstrapClone(ctx context.Context, c *pgx.Conn) (*pgx.Conn, error) {
	if !strictUnstampedLocalConnection(c.Config()) || os.Getenv("POSTGRES_USER") == "" || os.Getenv("POSTGRES_PASSWORD") == "" {
		return nil, errors.New("local fixture bootstrap credentials/configuration required")
	}
	clone := c.Config().Copy()
	clone.User, clone.Password = os.Getenv("POSTGRES_USER"), os.Getenv("POSTGRES_PASSWORD")
	b, err := pgx.ConnectConfig(ctx, clone)
	if err != nil {
		return nil, errors.New("local fixture bootstrap connection rejected")
	}
	if err = requireRecipientBootstrapIdentity(ctx, b, clone.User); err != nil {
		b.Close(ctx)
		return nil, err
	}
	return b, nil
}
