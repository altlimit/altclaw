package bridge

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"altclaw.ai/internal/config"
	"github.com/dop251/goja"
)

// RegisterDB adds the db.connect() / db.connections() bridge to the Goja runtime.
func RegisterDB(vm *goja.Runtime, pool *DBPool, store *config.Store, workspace string, ctxFn ...func() context.Context) {
	dbObj := vm.NewObject()
	getCtx := defaultCtxFn(ctxFn)

	// db.connect(driver, connStr) → handle object with .query(), .exec(), .close()
	dbObj.Set("connect", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "db.connect requires (driver, connectionString)")
		}
		driver := call.Arguments[0].String()
		connStr := ExpandSecrets(getCtx(), store, call.Arguments[1].String())

		conn, err := pool.Get(driver, connStr)
		if err != nil {
			logErr(vm, "db.connect", err)
		}

		key := driver + ":" + call.Arguments[1].String() // use unexpanded for close key

		return makeConnHandle(vm, conn, pool, key)
	})

	// db.connections() → array of active connection keys
	dbObj.Set("connections", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(pool.List())
	})

	vm.Set(NameDB, dbObj)
}

// makeConnHandle builds a JS object with query/exec/close methods bound to the given *sql.DB.
func makeConnHandle(vm *goja.Runtime, conn *sql.DB, pool *DBPool, key string) goja.Value {
	handle := vm.NewObject()

	// handle.query(sql, params?, callback?) → object[] | number
	// Without callback: returns array of row objects (buffered).
	// With callback: streams rows one-by-one, returns count. Return false to stop early.
	handle.Set("query", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "query requires (sql, params?, callback?)")
		}
		sqlStr := call.Arguments[0].String()

		var params []interface{}
		var callback goja.Callable

		// Parse args: (sql), (sql, params), (sql, callback), (sql, params, callback)
		for i := 1; i < len(call.Arguments); i++ {
			if cb, ok := goja.AssertFunction(call.Arguments[i]); ok {
				callback = cb
				break
			}
			if params == nil {
				params = extractParams(vm, call, i)
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		rows, err := conn.QueryContext(ctx, sqlStr, params...)
		if err != nil {
			logErr(vm, "db.query", err)
		}
		defer rows.Close()

		cols, err := rows.Columns()
		if err != nil {
			logErr(vm, "db.query", err)
		}

		// Streaming mode: call callback for each row
		if callback != nil {
			var count int64
			for rows.Next() {
				values := make([]interface{}, len(cols))
				ptrs := make([]interface{}, len(cols))
				for i := range values {
					ptrs[i] = &values[i]
				}
				if err := rows.Scan(ptrs...); err != nil {
					logErr(vm, "db.query", err)
				}

				row := make(map[string]interface{}, len(cols))
				for i, col := range cols {
					row[col] = convertValue(values[i])
				}

				count++
				ret, err := callback(goja.Undefined(), vm.ToValue(row))
				if err != nil {
					Throw(vm, fmt.Sprintf("db.query callback error: %v", err))
				}
				if ret != nil && !goja.IsUndefined(ret) && !goja.IsNull(ret) && !ret.ToBoolean() {
					break
				}
			}
			if err := rows.Err(); err != nil {
				logErr(vm, "db.query", err)
			}
			return vm.ToValue(count)
		}

		// Buffered mode: load all rows
		var results []interface{}
		for rows.Next() {
			values := make([]interface{}, len(cols))
			ptrs := make([]interface{}, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				logErr(vm, "db.query", err)
			}

			row := make(map[string]interface{}, len(cols))
			for i, col := range cols {
				row[col] = convertValue(values[i])
			}
			results = append(results, row)
		}
		if err := rows.Err(); err != nil {
			logErr(vm, "db.query", err)
		}
		if results == nil {
			results = []interface{}{}
		}
		return vm.ToValue(results)
	})

	// handle.exec(sql, params?) → {rowsAffected, lastInsertId}
	handle.Set("exec", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "exec requires (sql, params?)")
		}
		sqlStr := call.Arguments[0].String()
		params := extractParams(vm, call, 1)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		result, err := conn.ExecContext(ctx, sqlStr, params...)
		if err != nil {
			logErr(vm, "db.exec", err)
		}

		affected, _ := result.RowsAffected()
		lastID, _ := result.LastInsertId()

		obj := vm.NewObject()
		obj.Set("rowsAffected", affected)
		obj.Set("lastInsertId", lastID)
		return obj
	})

	// handle.close() — force-evicts this connection from the pool
	handle.Set("close", func(call goja.FunctionCall) goja.Value {
		pool.Close(key)
		return goja.Undefined()
	})

	return MethodProxy(vm, NameDB, "db connection", handle)
}

// extractParams pulls query parameters from the JS arguments at the given index.
// Expects an array argument; returns nil if not provided.
func extractParams(vm *goja.Runtime, call goja.FunctionCall, idx int) []interface{} {
	if idx >= len(call.Arguments) {
		return nil
	}
	arg := call.Arguments[idx]
	if arg == nil || goja.IsUndefined(arg) || goja.IsNull(arg) {
		return nil
	}
	obj := arg.ToObject(vm)
	length := obj.Get("length")
	if length == nil || goja.IsUndefined(length) {
		return nil
	}
	n := int(length.ToInteger())
	params := make([]interface{}, n)
	for i := 0; i < n; i++ {
		val := obj.Get(fmt.Sprintf("%d", i))
		if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
			params[i] = nil
		} else {
			params[i] = val.Export()
		}
	}
	return params
}

// convertValue converts a sql.Scan result into a JS-friendly Go type.
func convertValue(v interface{}) interface{} {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case []byte:
		return string(val)
	case time.Time:
		return val.Format(time.RFC3339)
	default:
		return val
	}
}
