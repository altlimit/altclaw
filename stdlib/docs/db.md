### [ db ] - Database Connections
Connect to databases directly. Supports SQLite, PostgreSQL, MySQL, and Microsoft SQL Server.
Connections are auto-pooled and close after 5 minutes of inactivity.

[ Connect ]
* db.connect(driver: string, connStr: string) → Connection
  - driver: "sqlite", "postgres", "mysql", or "mssql"
  - connStr: Native connection string for the driver
  - Connection strings support {{secrets.NAME}} placeholders
  - SQLite paths are jailed to the workspace directory

  Examples:
    db.connect("sqlite", "./data.db")
    db.connect("sqlite", ":memory:")
    db.connect("postgres", "postgres://user:pass@host:5432/dbname")
    db.connect("mysql", "user:pass@tcp(host:3306)/dbname")
    db.connect("mssql", "sqlserver://user:pass@host?database=dbname")

[ Connection Methods ]
* conn.query(sql: string, params?: any[], callback?: (row: object) => boolean|void) → object[] | number
  - Without callback: returns array of row objects (column names as keys)
  - With callback: streams rows one-by-one without loading all into memory, returns row count
  - Return false from callback to stop iteration early
  - Always use ? placeholders for parameters (prevents SQL injection)
  - Examples:
    conn.query("SELECT * FROM users WHERE age > ?", [21])
    conn.query("SELECT * FROM big_table", function(row) {
      output(row.name);
    });
    conn.query("SELECT * FROM logs WHERE level = ?", ["error"], function(row) {
      if (row.id > 1000) return false; // stop early
    });

* conn.exec(sql: string, params?: any[]) → {rowsAffected: number, lastInsertId: number}
  - For INSERT, UPDATE, DELETE, CREATE TABLE, etc.
  - Example: conn.exec("INSERT INTO users (name) VALUES (?)", ["Alice"])

* conn.close() → void
  - Force-closes this connection (optional — auto-closes after idle timeout)

[ Global ]
* db.connections() → string[]
  - List all active connection keys (e.g. ["sqlite:./data.db"])
