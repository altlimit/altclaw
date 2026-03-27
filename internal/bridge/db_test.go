package bridge

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dop251/goja"
)

func setupDBVM(t *testing.T) (*goja.Runtime, string, *DBPool) {
	t.Helper()
	vm := goja.New()
	workspace := t.TempDir()
	pool := NewDBPool(workspace)
	t.Cleanup(func() { pool.CloseAll() })
	RegisterDB(vm, pool, nil, workspace)
	return vm, workspace, pool
}

func TestDB_QueryBasic(t *testing.T) {
	vm, _, _ := setupDBVM(t)

	_, err := vm.RunString(`
		var c = db.connect("sqlite", ":memory:");
		c.exec("CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, age INTEGER)");
		c.exec("INSERT INTO users (name, age) VALUES (?, ?)", ["Alice", 30]);
		c.exec("INSERT INTO users (name, age) VALUES (?, ?)", ["Bob", 25]);
	`)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	val, err := vm.RunString(`
		var rows = c.query("SELECT * FROM users ORDER BY id");
		rows.length + "|" + rows[0].name + "|" + rows[0].age + "|" + rows[1].name + "|" + rows[1].age;
	`)
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	expected := "2|Alice|30|Bob|25"
	if val.String() != expected {
		t.Errorf("expected %s, got %s", expected, val.String())
	}
}

func TestDB_ExecInsert(t *testing.T) {
	vm, _, _ := setupDBVM(t)

	val, err := vm.RunString(`
		var c = db.connect("sqlite", ":memory:");
		c.exec("CREATE TABLE items (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT)");
		var r = c.exec("INSERT INTO items (name) VALUES (?)", ["widget"]);
		JSON.stringify({rowsAffected: r.rowsAffected, lastInsertId: r.lastInsertId});
	`)
	if err != nil {
		t.Fatalf("exec failed: %v", err)
	}
	expected := `{"rowsAffected":1,"lastInsertId":1}`
	if val.String() != expected {
		t.Errorf("expected %s, got %s", expected, val.String())
	}
}

func TestDB_ParameterizedQuery(t *testing.T) {
	vm, _, _ := setupDBVM(t)

	val, err := vm.RunString(`
		var c = db.connect("sqlite", ":memory:");
		c.exec("CREATE TABLE t (v TEXT)");
		c.exec("INSERT INTO t VALUES (?)", ["hello"]);
		c.exec("INSERT INTO t VALUES (?)", ["world"]);
		// This should match only "hello", not be vulnerable to injection
		var rows = c.query("SELECT * FROM t WHERE v = ?", ["hello"]);
		rows.length;
	`)
	if err != nil {
		t.Fatalf("parameterized query failed: %v", err)
	}
	if val.String() != "1" {
		t.Errorf("expected 1 row, got %s", val.String())
	}
}

func TestDB_ConnectionReuse(t *testing.T) {
	vm, workspace, pool := setupDBVM(t)

	// Create a DB file so the path is valid
	dbPath := filepath.Join(workspace, "reuse.db")
	os.WriteFile(dbPath, []byte{}, 0644)

	_, err := vm.RunString(`
		var c1 = db.connect("sqlite", "reuse.db");
		c1.exec("CREATE TABLE IF NOT EXISTS t (id INTEGER)");
		var c2 = db.connect("sqlite", "reuse.db");
		c2.exec("INSERT INTO t VALUES (1)");
	`)
	if err != nil {
		t.Fatalf("connection reuse failed: %v", err)
	}

	// Pool should have exactly one entry for this connection
	list := pool.List()
	count := 0
	for _, k := range list {
		if k == "sqlite:reuse.db" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 pool entry for sqlite:reuse.db, got %d (list: %v)", count, list)
	}
}

func TestDB_SQLitePathJail(t *testing.T) {
	vm, _, _ := setupDBVM(t)

	_, err := vm.RunString(`db.connect("sqlite", "../../etc/passwd")`)
	if err == nil {
		t.Fatal("expected path jail error, got nil")
	}
}

func TestDB_InvalidDriver(t *testing.T) {
	vm, _, _ := setupDBVM(t)

	_, err := vm.RunString(`db.connect("mongodb", "localhost:27017")`)
	if err == nil {
		t.Fatal("expected error for unknown driver, got nil")
	}
}

func TestDB_CloseAndReconnect(t *testing.T) {
	vm, _, pool := setupDBVM(t)

	_, err := vm.RunString(`
		var c = db.connect("sqlite", ":memory:");
		c.exec("CREATE TABLE t (v TEXT)");
		c.close();
	`)
	if err != nil {
		t.Fatalf("close failed: %v", err)
	}

	// Pool should be empty after close
	if len(pool.List()) != 0 {
		t.Errorf("expected empty pool after close, got %v", pool.List())
	}

	// Should be able to reconnect (new in-memory DB)
	_, err = vm.RunString(`
		var c2 = db.connect("sqlite", ":memory:");
		c2.exec("CREATE TABLE t2 (v TEXT)");
	`)
	if err != nil {
		t.Fatalf("reconnect failed: %v", err)
	}
}

func TestDB_ConnectionsList(t *testing.T) {
	vm, _, _ := setupDBVM(t)

	val, err := vm.RunString(`
		db.connect("sqlite", ":memory:");
		JSON.stringify(db.connections());
	`)
	if err != nil {
		t.Fatalf("connections list failed: %v", err)
	}
	expected := `["sqlite::memory:"]`
	if val.String() != expected {
		t.Errorf("expected %s, got %s", expected, val.String())
	}
}

func TestDB_PoolIdleEviction(t *testing.T) {
	workspace := t.TempDir()
	pool := NewDBPool(workspace)
	defer pool.CloseAll()

	// Override idle timeout to something very short for testing
	pool.idleTimeout = 50 * time.Millisecond

	// Open a connection
	_, err := pool.Get("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open connection: %v", err)
	}
	if len(pool.List()) != 1 {
		t.Fatalf("expected 1 connection, got %d", len(pool.List()))
	}

	// Wait for idle timeout + reap
	time.Sleep(100 * time.Millisecond)
	pool.reap() // manually trigger reap instead of waiting for ticker

	if len(pool.List()) != 0 {
		t.Errorf("expected 0 connections after eviction, got %d", len(pool.List()))
	}
}

func TestDB_EmptyQueryResult(t *testing.T) {
	vm, _, _ := setupDBVM(t)

	val, err := vm.RunString(`
		var c = db.connect("sqlite", ":memory:");
		c.exec("CREATE TABLE t (v TEXT)");
		var rows = c.query("SELECT * FROM t");
		JSON.stringify(rows);
	`)
	if err != nil {
		t.Fatalf("empty query failed: %v", err)
	}
	if val.String() != "[]" {
		t.Errorf("expected [], got %s", val.String())
	}
}
