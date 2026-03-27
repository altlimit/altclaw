package bridge

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	// Pure-Go database drivers — imported for side-effect registration.
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/microsoft/go-mssqldb"
	_ "modernc.org/sqlite"
)

// driverMap translates user-facing driver names to Go sql.Open driver strings.
var driverMap = map[string]string{
	"sqlite":   "sqlite",
	"postgres": "pgx",
	"mysql":    "mysql",
	"mssql":    "sqlserver",
}

// poolEntry holds a database connection and its last-access time.
type poolEntry struct {
	db         *sql.DB
	lastAccess time.Time
}

// DBPool manages a set of database connections keyed by "driver:connStr".
// Idle connections are automatically closed after idleTimeout.
type DBPool struct {
	mu          sync.Mutex
	conns       map[string]*poolEntry
	workspace   string
	idleTimeout time.Duration
	stopReaper  chan struct{}
}

// NewDBPool creates a new connection pool. The reaper goroutine runs in the
// background and closes connections that have been idle for more than 5 minutes.
func NewDBPool(workspace string) *DBPool {
	p := &DBPool{
		conns:       make(map[string]*poolEntry),
		workspace:   workspace,
		idleTimeout: 5 * time.Minute,
		stopReaper:  make(chan struct{}),
	}
	go p.reapLoop()
	return p
}

// Get returns an existing or new *sql.DB for the given driver and connection string.
func (p *DBPool) Get(driver, connStr string) (*sql.DB, error) {
	goDriver, ok := driverMap[strings.ToLower(driver)]
	if !ok {
		return nil, fmt.Errorf("db: unsupported driver %q (use sqlite, postgres, mysql, or mssql)", driver)
	}

	// For SQLite, jail the path to the workspace.
	// Skip special in-memory DSNs that aren't actual file paths.
	actualConnStr := connStr
	if goDriver == "sqlite" {
		if connStr != ":memory:" && !strings.HasPrefix(connStr, "file::memory:") {
			safePath, err := SanitizePath(p.workspace, connStr)
			if err != nil {
				return nil, fmt.Errorf("db: sqlite path not allowed: %w", err)
			}
			actualConnStr = safePath
		}
	}

	key := driver + ":" + connStr
	p.mu.Lock()
	defer p.mu.Unlock()

	if entry, exists := p.conns[key]; exists {
		// Ping to verify the connection is still alive
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := entry.db.PingContext(ctx); err != nil {
			// Connection is dead — remove and re-open below
			slog.Debug("db pool: stale connection, reopening", "key", key, "err", err)
			entry.db.Close()
			delete(p.conns, key)
		} else {
			entry.lastAccess = time.Now()
			return entry.db, nil
		}
	}

	db, err := sql.Open(goDriver, actualConnStr)
	if err != nil {
		return nil, fmt.Errorf("db: failed to open %s connection: %w", driver, err)
	}

	// Sensible pool limits for an embedded agent context
	db.SetMaxOpenConns(5)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(5 * time.Minute)

	// Verify the connection works
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("db: failed to connect to %s: %w", driver, err)
	}

	p.conns[key] = &poolEntry{db: db, lastAccess: time.Now()}
	slog.Info("db pool: opened connection", "key", key)
	return db, nil
}

// Close force-closes a single connection by key.
func (p *DBPool) Close(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry, ok := p.conns[key]; ok {
		entry.db.Close()
		delete(p.conns, key)
		slog.Info("db pool: closed connection", "key", key)
	}
}

// CloseAll closes all connections and stops the reaper goroutine.
func (p *DBPool) CloseAll() {
	close(p.stopReaper)
	p.mu.Lock()
	defer p.mu.Unlock()
	for key, entry := range p.conns {
		entry.db.Close()
		slog.Info("db pool: closed connection", "key", key)
	}
	p.conns = make(map[string]*poolEntry)
}

// List returns the keys of all active connections.
func (p *DBPool) List() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	keys := make([]string, 0, len(p.conns))
	for k := range p.conns {
		keys = append(keys, k)
	}
	return keys
}

// reapLoop periodically evicts idle connections.
func (p *DBPool) reapLoop() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.stopReaper:
			return
		case <-ticker.C:
			p.reap()
		}
	}
}

func (p *DBPool) reap() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for key, entry := range p.conns {
		if now.Sub(entry.lastAccess) > p.idleTimeout {
			entry.db.Close()
			delete(p.conns, key)
			slog.Info("db pool: evicted idle connection", "key", key)
		}
	}
}
