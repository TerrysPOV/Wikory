package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/TerrysPOV/Wikory/internal/confine"
	_ "modernc.org/sqlite"
)

var (
	ErrNoStore     = errors.New("store: no store for this project on this machine")
	ErrNewerSchema = errors.New("store: the store was written by a newer wikory; this binary will not write to it")
)

// Identity is everything that locates a project's store and a checkout within it.
type Identity struct {
	ProjectID string
	Origin    string
	CommonDir string
	Worktree  string
}

type registry struct {
	ProjectID  string   `toml:"project_id"`
	Origins    []string `toml:"origins"`
	CommonDirs []string `toml:"common_dirs"`
}

// Store is one project's private store on this machine.
type Store struct {
	Dir     string
	Key     string
	DataDir string
	DB      *sql.DB
	Root    *confine.Root
	reg     registry
}

var storeSubdirs = []string{"turns", "jobs", "locks", "logs"}

const (
	registryLockWait = 5 * time.Second
	maxRegistryFile  = 1 << 16
)

// addOrigin records origin when it is non-empty and new, and reports whether it changed r.
func (r *registry) addOrigin(origin string) bool {
	if origin == "" || slices.Contains(r.Origins, origin) {
		return false
	}
	r.Origins = append(r.Origins, origin)
	return true
}

// addCommonDir records commonDir when it is new, and reports whether it changed r.
func (r *registry) addCommonDir(commonDir string) bool {
	if slices.Contains(r.CommonDirs, commonDir) {
		return false
	}
	r.CommonDirs = append(r.CommonDirs, commonDir)
	return true
}

func lockRegistry(dataDir string) (func(), error) {
	return Lock(filepath.Join(dataDir, "registry.lock"), registryLockWait)
}

// Resolve finds the store for id, creating it when create is true. It never registers a checkout.
func Resolve(ctx context.Context, dataDir string, id Identity, create bool) (*Store, error) {
	RestrictUmask()
	stores := filepath.Join(dataDir, "stores")
	for _, d := range []string{dataDir, stores} {
		if err := EnsurePrivateDir(d); err != nil {
			return nil, err
		}
	}
	unlock, err := lockRegistry(dataDir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	key, reg, found, err := findStore(stores, id)
	if err != nil {
		return nil, err
	}
	var dirty bool
	if found {
		// A new origin is reachable only when found through a registered common directory.
		dirty = reg.addOrigin(id.Origin)
	} else {
		if !create {
			return nil, ErrNoStore
		}
		key = StoreKey(id.ProjectID, id.Origin, id.CommonDir)
		reg = registry{ProjectID: id.ProjectID}
		reg.addOrigin(id.Origin)
		dirty = true
	}
	dir := filepath.Join(stores, key)
	if err := EnsurePrivateDir(dir); err != nil {
		return nil, err
	}
	for _, sub := range storeSubdirs {
		if err := EnsurePrivateDir(filepath.Join(dir, sub)); err != nil {
			return nil, err
		}
	}
	root, err := confine.Open(dir)
	if err != nil {
		return nil, err
	}
	s := &Store{Dir: dir, Key: key, DataDir: dataDir, Root: root, reg: reg}
	if dirty {
		if err := s.writeRegistry(); err != nil {
			root.Close()
			return nil, err
		}
	}
	if s.DB, err = openDB(ctx, filepath.Join(dir, "wikory.db")); err != nil {
		root.Close()
		return nil, err
	}
	return s, nil
}

func findStore(stores string, id Identity) (string, registry, bool, error) {
	root, err := confine.Open(stores)
	if err != nil {
		return "", registry{}, false, err
	}
	defer root.Close()
	ents, err := root.ReadDir(".")
	if err != nil {
		return "", registry{}, false, err
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		data, err := root.ReadFile(e.Name()+"/store.toml", maxRegistryFile)
		if err != nil {
			continue // a half-created store without a registry is not a match
		}
		var reg registry
		if toml.Unmarshal(data, &reg) != nil || reg.ProjectID != id.ProjectID {
			continue
		}
		if (id.Origin != "" && slices.Contains(reg.Origins, id.Origin)) || slices.Contains(reg.CommonDirs, id.CommonDir) {
			return e.Name(), reg, true, nil
		}
	}
	return "", registry{}, false, nil
}

func (s *Store) writeRegistry() error {
	data, err := toml.Marshal(s.reg)
	if err != nil {
		return err
	}
	return s.Root.WriteFileAtomic("store.toml", data, 0o600)
}

// updateRegistry re-reads store.toml under the registry lock, applies change, and writes it.
func (s *Store) updateRegistry(change func(*registry) bool) error {
	unlock, err := lockRegistry(s.DataDir)
	if err != nil {
		return err
	}
	defer unlock()
	data, err := s.Root.ReadFile("store.toml", maxRegistryFile)
	if err != nil {
		return err
	}
	var reg registry
	if err := toml.Unmarshal(data, &reg); err != nil {
		return err
	}
	if !change(&reg) {
		return nil
	}
	s.reg = reg
	return s.writeRegistry()
}

// Origins lists every canonical origin this store has been known by.
func (s *Store) Origins() []string { return slices.Clone(s.reg.Origins) }

func openDB(ctx context.Context, path string) (*sql.DB, error) {
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: schema: %w", err)
	}
	var v string
	err = db.QueryRowContext(ctx, `SELECT v FROM meta WHERE k = 'schema_version'`).Scan(&v)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = db.ExecContext(ctx, `INSERT INTO meta(k, v) VALUES('schema_version', ?)`, strconv.Itoa(SchemaVersion))
	case err == nil:
		if n, _ := strconv.Atoi(v); n > SchemaVersion {
			err = ErrNewerSchema
		}
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Close releases the database and the store root.
func (s *Store) Close() error {
	err := s.DB.Close()
	if cerr := s.Root.Close(); err == nil {
		err = cerr
	}
	return err
}

// Bump increments a named counter (hook errors, refusals, fallbacks).
func (s *Store) Bump(ctx context.Context, name string) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO counters(name, n) VALUES(?, 1) ON CONFLICT(name) DO UPDATE SET n = n + 1`, name)
	return err
}

// Counter reads a named counter, zero when absent.
func (s *Store) Counter(ctx context.Context, name string) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT n FROM counters WHERE name = ?`, name).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
