package bridge

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/dop251/goja"
)

// RegisterCSV adds the csv namespace (csv.read, csv.write, csv.append) to the runtime.
// All paths are jailed to the given workspace directory.
func RegisterCSV(vm *goja.Runtime, workspace string) {
	csvObj := vm.NewObject()

	sanitize := func(path string) (string, error) {
		return SanitizePath(workspace, path)
	}

	// csv.read(path, opts?, callback?) → object[]|string[][]|number
	// Without callback: loads all rows into memory and returns them.
	// With callback: streams rows one-by-one, returns count of processed rows.
	// opts.header (default true): if true, first row is used as column names → rows are objects.
	//                             if false, rows are arrays of strings.
	// opts.delimiter (default ","): field separator character.
	// opts.skip (default 0): number of data rows to skip after header.
	// opts.comment (default ""): lines starting with this character are ignored.
	csvObj.Set("read", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "csv.read requires a path argument")
		}
		path, err := sanitize(call.Arguments[0].String())
		if err != nil {
			logErr(vm, "csv.read", err)
		}

		// Parse optional arguments: (path, opts?, callback?)
		header := true
		delimiter := ','
		skip := 0
		comment := rune(0)
		var callback goja.Callable

		argIdx := 1
		if argIdx < len(call.Arguments) {
			if cb, ok := goja.AssertFunction(call.Arguments[argIdx]); ok {
				callback = cb
			} else if !goja.IsUndefined(call.Arguments[argIdx]) && !goja.IsNull(call.Arguments[argIdx]) {
				if opts, ok := call.Arguments[argIdx].Export().(map[string]interface{}); ok {
					if v, ok := opts["header"]; ok {
						if b, ok := v.(bool); ok {
							header = b
						}
					}
					if v, ok := opts["delimiter"]; ok {
						if s, ok := v.(string); ok && len(s) > 0 {
							delimiter = rune(s[0])
						}
					}
					if v, ok := opts["skip"]; ok {
						if n, ok := v.(int64); ok {
							skip = int(n)
						} else if n, ok := v.(float64); ok {
							skip = int(n)
						}
					}
					if v, ok := opts["comment"]; ok {
						if s, ok := v.(string); ok && len(s) > 0 {
							comment = rune(s[0])
						}
					}
				}
				argIdx++
				if argIdx < len(call.Arguments) {
					if cb, ok := goja.AssertFunction(call.Arguments[argIdx]); ok {
						callback = cb
					}
				}
			}
		}

		f, err := os.Open(path)
		if err != nil {
			logErr(vm, "csv.read", err)
		}
		defer f.Close()

		reader := csv.NewReader(f)
		reader.Comma = delimiter
		if comment != 0 {
			reader.Comment = comment
		}
		reader.LazyQuotes = true
		reader.FieldsPerRecord = -1 // allow variable field counts

		// Read header row if enabled
		var columns []string
		if header {
			columns, err = reader.Read()
			if err != nil {
				logErr(vm, "csv.read", err)
			}
		}

		// Skip rows
		for i := 0; i < skip; i++ {
			if _, err := reader.Read(); err != nil {
				if err == io.EOF {
					break
				}
				logErr(vm, "csv.read", err)
			}
		}

		// Build a row value from a CSV record
		makeRow := func(record []string) goja.Value {
			if header && columns != nil {
				row := make(map[string]interface{}, len(columns))
				for i, col := range columns {
					if i < len(record) {
						row[col] = record[i]
					} else {
						row[col] = ""
					}
				}
				return vm.ToValue(row)
			}
			vals := make([]interface{}, len(record))
			for i, v := range record {
				vals[i] = v
			}
			return vm.ToValue(vals)
		}

		// Streaming mode with callback
		if callback != nil {
			var count int64
			for {
				record, err := reader.Read()
				if err == io.EOF {
					break
				}
				if err != nil {
					logErr(vm, "csv.read", err)
				}
				count++
				ret, callErr := callback(goja.Undefined(), makeRow(record))
				if callErr != nil {
					Throw(vm, fmt.Sprintf("csv.read callback error: %v", callErr))
				}
				if ret != nil && !goja.IsUndefined(ret) && !goja.IsNull(ret) && !ret.ToBoolean() {
					break
				}
			}
			return vm.ToValue(count)
		}

		// Buffered mode: load all rows
		var results []interface{}
		for {
			record, err := reader.Read()
			if err == io.EOF {
				break
			}
			if err != nil {
				logErr(vm, "csv.read", err)
			}
			results = append(results, makeRow(record))
		}
		if results == nil {
			results = []interface{}{}
		}
		return vm.ToValue(results)
	})

	// csv.write(path, rows, opts?) — write/overwrite a CSV file
	csvObj.Set("write", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "csv.write requires path and rows arguments")
		}
		path, err := sanitize(call.Arguments[0].String())
		if err != nil {
			logErr(vm, "csv.write", err)
		}

		delimiter, columns := parseWriteOpts(vm, call, 2)
		rows := exportRows(vm, call.Arguments[1])

		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			logErr(vm, "csv.write", err)
		}

		f, err := os.Create(path)
		if err != nil {
			logErr(vm, "csv.write", err)
		}
		defer f.Close()

		writer := csv.NewWriter(f)
		writer.Comma = delimiter

		// Determine columns from first row if not explicitly provided
		if columns == nil && len(rows) > 0 {
			if obj, ok := rows[0].(map[string]interface{}); ok {
				columns = sortedKeys(obj)
			}
		}

		// Write header if we have object rows
		if columns != nil {
			if err := writer.Write(columns); err != nil {
				logErr(vm, "csv.write", err)
			}
		}

		writeRows(vm, writer, rows, columns, "csv.write")
		writer.Flush()
		if err := writer.Error(); err != nil {
			logErr(vm, "csv.write", err)
		}
		return goja.Undefined()
	})

	// csv.append(path, rows, opts?) — append rows to an existing CSV file (no header)
	csvObj.Set("append", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "csv.append requires path and rows arguments")
		}
		path, err := sanitize(call.Arguments[0].String())
		if err != nil {
			logErr(vm, "csv.append", err)
		}

		delimiter, columns := parseWriteOpts(vm, call, 2)
		rows := exportRows(vm, call.Arguments[1])

		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			logErr(vm, "csv.append", err)
		}

		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			logErr(vm, "csv.append", err)
		}
		defer f.Close()

		writer := csv.NewWriter(f)
		writer.Comma = delimiter

		// For append with objects, we need column order.
		// Read from existing file header if not provided.
		if columns == nil && len(rows) > 0 {
			if _, ok := rows[0].(map[string]interface{}); ok {
				columns = readExistingHeader(path, delimiter)
				if columns == nil {
					// Fallback: use keys from first row
					if obj, ok := rows[0].(map[string]interface{}); ok {
						columns = sortedKeys(obj)
					}
				}
			}
		}

		writeRows(vm, writer, rows, columns, "csv.append")
		writer.Flush()
		if err := writer.Error(); err != nil {
			logErr(vm, "csv.append", err)
		}
		return goja.Undefined()
	})

	vm.Set(NameCSV, csvObj)
}

// parseWriteOpts extracts delimiter and columns from the options argument.
func parseWriteOpts(vm *goja.Runtime, call goja.FunctionCall, idx int) (rune, []string) {
	delimiter := ','
	var columns []string
	if idx < len(call.Arguments) && !goja.IsUndefined(call.Arguments[idx]) && !goja.IsNull(call.Arguments[idx]) {
		if opts, ok := call.Arguments[idx].Export().(map[string]interface{}); ok {
			if v, ok := opts["delimiter"]; ok {
				if s, ok := v.(string); ok && len(s) > 0 {
					delimiter = rune(s[0])
				}
			}
			if v, ok := opts["columns"]; ok {
				if arr, ok := v.([]interface{}); ok {
					columns = make([]string, len(arr))
					for i, c := range arr {
						columns[i] = fmt.Sprintf("%v", c)
					}
				}
			}
		}
	}
	return delimiter, columns
}

// exportRows converts a JS array value into a Go slice of row data.
func exportRows(vm *goja.Runtime, val goja.Value) []interface{} {
	exported := val.Export()
	if arr, ok := exported.([]interface{}); ok {
		return arr
	}
	Throw(vm, "rows must be an array")
	return nil
}

// writeRows writes row data to a CSV writer.
func writeRows(vm *goja.Runtime, writer *csv.Writer, rows []interface{}, columns []string, label string) {
	for _, row := range rows {
		var record []string
		switch r := row.(type) {
		case map[string]interface{}:
			if columns == nil {
				columns = sortedKeys(r)
			}
			record = make([]string, len(columns))
			for i, col := range columns {
				record[i] = fmt.Sprintf("%v", r[col])
			}
		case []interface{}:
			record = make([]string, len(r))
			for i, v := range r {
				record[i] = fmt.Sprintf("%v", v)
			}
		default:
			Throw(vm, fmt.Sprintf("%s: invalid row type", label))
		}
		if err := writer.Write(record); err != nil {
			logErr(vm, label, err)
		}
	}
}

// sortedKeys returns the keys of a map in sorted order for deterministic column output.
func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// readExistingHeader reads the first row of an existing CSV to get column names.
func readExistingHeader(path string, delimiter rune) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	reader := csv.NewReader(f)
	reader.Comma = delimiter
	reader.LazyQuotes = true
	header, err := reader.Read()
	if err != nil {
		return nil
	}
	return header
}
