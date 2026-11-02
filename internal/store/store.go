package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
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
	// CloneID is the verified nonce of the clone (see CloneID); it is part of every checkout id.
	CloneID string
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
	// Notices are things the caller should tell the user about how this store was resolved.
	Notices []string
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
	var notices []string
	if found {
		// A new origin is reachable only when found through a registered common directory,
		// and only when no other store already owns it: aliasing would let the clone flip
		// between the two stores and lose its memory.
		if id.Origin != "" && !slices.Contains(reg.Origins, id.Origin) {
			owned, err := originOwnedElsewhere(stores, id.ProjectID, key, id.Origin)
			if err != nil {
				return nil, err
			}
			if owned {
				notices = append(notices, aliasNotice(id.Origin))
			} else {
				dirty = reg.addOrigin(id.Origin)
			}
		}
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
	s := &Store{Dir: dir, Key: key, DataDir: dataDir, Root: root, reg: reg, Notices: notices}
	// The database is opened first: a newer schema is refused before anything is written.
	if s.DB, err = openDB(ctx, filepath.Join(dir, "wikory.db"), WriteLockPath(filepath.Join(dir, "locks"))); err != nil {
		root.Close()
		return nil, err
	}
	if dirty {
		if err := s.writeRegistry(); err != nil {
			s.DB.Close()
			root.Close()
			return nil, err
		}
	}
	return s, nil
}

type storedRegistry struct {
	key string
	reg registry
}

// scanStores lists every readable store registry of project projectID, in key order.
func scanStores(stores, projectID string) ([]storedRegistry, error) {
	root, err := confine.Open(stores)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	ents, err := root.ReadDir(".")
	if err != nil {
		return nil, err
	}
	sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
	var out []storedRegistry
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		data, err := root.ReadFile(e.Name()+"/store.toml", maxRegistryFile)
		if err != nil {
			continue // a half-created store without a registry is not a match
		}
		var reg registry
		if toml.Unmarshal(data, &reg) != nil || reg.ProjectID != projectID {
			continue
		}
		out = append(out, storedRegistry{e.Name(), reg})
	}
	return out, nil
}

// findStore prefers a store that already lists this clone's common directory. Only when none
// does is a store matched by origin, so a clone that gains another clone's origin keeps the
// store that holds its own memory, whatever order the store keys sort in.
func findStore(stores string, id Identity) (string, registry, bool, error) {
	all, err := scanStores(stores, id.ProjectID)
	if err != nil {
		return "", registry{}, false, err
	}
	for _, c := range all {
		if slices.Contains(c.reg.CommonDirs, id.CommonDir) {
			return c.key, c.reg, true, nil
		}
	}
	for _, c := range all {
		if id.Origin != "" && slices.Contains(c.reg.Origins, id.Origin) {
			return c.key, c.reg, true, nil
		}
	}
	return "", registry{}, false, nil
}

// originOwnedElsewhere reports whether a store other than key already lists origin.
func originOwnedElsewhere(stores, projectID, key, origin string) (bool, error) {
	all, err := scanStores(stores, projectID)
	if err != nil {
		return false, err
	}
	for _, c := range all {
		if c.key != key && slices.Contains(c.reg.Origins, origin) {
			return true, nil
		}
	}
	return false, nil
}

func aliasNotice(origin string) string {
	return "this clone's remote " + origin + " is already used by another clone's private store, so the two stores are kept separate"
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

// openDB opens the database and brings its schema up to date. The schema statements are
// writes, so they run inside the database write lock (lockPath), which Resolve takes after
// the registry lock.
func openDB(ctx context.Context, path, lockPath string) (*sql.DB, error) {
	dsn := "file:" + filepath.ToSlash(path) +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)&_pragma=foreign_keys(1)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	// A newer schema is refused before the schema statements run, so a store this binary
	// will not write to is not written to.
	var hasMeta int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'meta'`).Scan(&hasMeta); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: schema: %w", err)
	}
	var v string
	err = sql.ErrNoRows
	if hasMeta > 0 {
		err = db.QueryRowContext(ctx, `SELECT v FROM meta WHERE k = 'schema_version'`).Scan(&v)
		if err == nil {
			if n, _ := strconv.Atoi(v); n > SchemaVersion {
				db.Close()
				return nil, ErrNewerSchema
			}
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		db.Close()
		return nil, err
	}
	missingVersion := errors.Is(err, sql.ErrNoRows)
	applySchema := func(ctx context.Context) error {
		if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
			return fmt.Errorf("store: schema: %w", err)
		}
		if missingVersion {
			_, err := db.ExecContext(ctx, `INSERT INTO meta(k, v) VALUES('schema_version', ?) ON CONFLICT(k) DO NOTHING`, strconv.Itoa(SchemaVersion))
			return err
		}
		return nil
	}
	if !missingVersion && v == strconv.Itoa(SchemaVersion) {
		complete, cerr := schemaComplete(ctx, db)
		if cerr != nil {
			db.Close()
			return nil, cerr
		}
		if !complete {
			// A table or index is missing from a database that says it is up to date: creating
			// it writes, so it runs inside the lock like any other schema change.
			if err := WithWriteLockAt(ctx, lockPath, applySchema); err != nil {
				db.Close()
				return nil, err
			}
			return db, nil
		}
		// Every statement is CREATE ... IF NOT EXISTS on an object that exists, which SQLite
		// resolves while compiling the statement, without a write transaction. Taking the write
		// lock here would only make every open wait behind another process's write, so a store
		// that is up to date opens without it.
		if err := applySchema(ctx); err != nil {
			db.Close()
			return nil, err
		}
		return db, nil
	}
	// Creating or upgrading the schema writes, so it runs inside the database write lock.
	if err := WithWriteLockAt(ctx, lockPath, applySchema); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// schemaObjectRe finds the object each schema statement creates.
var schemaObjectRe = regexp.MustCompile(`(?i)CREATE (?:VIRTUAL )?(?:TABLE|INDEX) IF NOT EXISTS (\w+)`)

// schemaComplete reports whether every table and index the schema statements create is
// already in the database, so that running them changes nothing.
func schemaComplete(ctx context.Context, db *sql.DB) (bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type IN ('table', 'index')`)
	if err != nil {
		return false, fmt.Errorf("store: schema: %w", err)
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		have[name] = true
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	for _, m := range schemaObjectRe.FindAllStringSubmatch(schemaSQL, -1) {
		if !have[m[1]] {
			return false, nil
		}
	}
	return true, nil
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
	return s.WithWriteLock(ctx, func(ctx context.Context) error {
		_, err := s.DB.ExecContext(ctx, `INSERT INTO counters(name, n) VALUES(?, 1) ON CONFLICT(name) DO UPDATE SET n = n + 1`, name)
		return err
	})
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
