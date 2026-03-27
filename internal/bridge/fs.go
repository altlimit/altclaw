package bridge

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"altclaw.ai/internal/config"
	"altclaw.ai/internal/search"
	"github.com/dop251/goja"
)

// newLargeScanner creates a bufio.Scanner with a 64KB initial buffer and 10MB max line size.
// Used by readLines, grep, and lineCount to handle large files consistently.
func newLargeScanner(f *os.File) *bufio.Scanner {
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)
	return scanner
}

// RegisterFS adds the fs namespace (fs.read, fs.write, fs.list) to the runtime.
// All operations are jailed to the given workspace directory.
// ws and handler are optional (nil = no security gate, e.g. in tests).
func RegisterFS(vm *goja.Runtime, workspace string, ws *config.Workspace, handler UIHandler, store ...*config.Store) {
	fs := vm.NewObject()

	// sanitize delegates to the shared SanitizePath.
	sanitize := func(path string) (string, error) {
		return SanitizePath(workspace, path)
	}

	// checkRestricted is a convenience wrapper for CheckRestricted.
	check := func(absPath, op string) {
		CheckRestricted(vm, workspace, absPath, op, ws, handler, store...)
	}

	fs.Set("read", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "fs.read requires a path argument")
		}
		path, err := sanitize(call.Arguments[0].String())
		if err != nil {
			logErr(vm, "fs.read", err)
		}
		check(path, "fs.read")
		data, err := os.ReadFile(path)
		if err != nil {
			logErr(vm, "fs.read", err)
		}
		return vm.ToValue(string(data))
	})

	fs.Set("write", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "fs.write requires path and content arguments")
		}
		path, err := sanitize(call.Arguments[0].String())
		if err != nil {
			logErr(vm, "fs.write", err)
		}

		// Create parent directories if needed
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			logErr(vm, "fs.write", err)
		}

		content := call.Arguments[1].String()
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			logErr(vm, "fs.write", err)
		}
		return goja.Undefined()
	})

	fs.Set("list", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "fs.list requires a directory argument")
		}
		path, err := sanitize(call.Arguments[0].String())
		if err != nil {
			logErr(vm, "fs.list", err)
		}

		// Check for detailed option: fs.list(dir, {detailed: true})
		detailed := false
		if len(call.Arguments) >= 2 {
			if opts, ok := call.Arguments[1].Export().(map[string]interface{}); ok {
				if v, ok := opts["detailed"]; ok {
					if b, ok := v.(bool); ok {
						detailed = b
					}
				}
			}
		}

		entries, err := os.ReadDir(path)
		if err != nil {
			logErr(vm, "fs.list", err)
		}
		if !detailed {
			names := make([]interface{}, len(entries))
			for i, e := range entries {
				names[i] = e.Name()
			}
			return vm.ToValue(names)
		}
		// Detailed: [{name, isDir, size, modified}]
		items := make([]interface{}, len(entries))
		for i, e := range entries {
			obj := vm.NewObject()
			obj.Set("name", e.Name())
			obj.Set("isDir", e.IsDir())
			if info, err := e.Info(); err == nil {
				obj.Set("size", info.Size())
				obj.Set("modified", info.ModTime().Unix())
			} else {
				obj.Set("size", 0)
				obj.Set("modified", 0)
			}
			items[i] = obj
		}
		return vm.ToValue(items)
	})

	fs.Set("search", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "fs.search requires a query string argument")
		}
		query := call.Arguments[0].String()
		if query == "" {
			return vm.ToValue([]string{})
		}

		realWorkspace, err := filepath.EvalSymlinks(workspace)
		if err != nil {
			realWorkspace = workspace
		}

		results, err := search.Workspace(realWorkspace, query, false)
		if err != nil {
			Throwf(vm, "fs.search error: %v", err)
		}

		paths := make([]string, len(results))
		for i, r := range results {
			paths[i] = r.Path
		}

		return vm.ToValue(paths)
	})

	fs.Set("rm", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "fs.rm requires a path argument")
		}
		path, err := sanitize(call.Arguments[0].String())
		if err != nil {
			logErr(vm, "fs.rm", err)
		}
		check(path, "fs.rm")
		if err := os.RemoveAll(path); err != nil {
			logErr(vm, "fs.rm", err)
		}
		return goja.Undefined()
	})

	// fs.patch(path, oldText, newText, opts?) — surgical text replacement
	// Replaces occurrence(s) of oldText with newText.
	// By default fails if oldText is not found or matches multiple times (safety).
	// Pass {all: true} to replace every occurrence.
	fs.Set("patch", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 3 {
			Throw(vm, "fs.patch requires path, oldText, and newText arguments")
		}
		path, err := sanitize(call.Arguments[0].String())
		if err != nil {
			logErr(vm, "fs.patch", err)
		}
		oldText := call.Arguments[1].String()
		newText := call.Arguments[2].String()

		// Check for {all: true} option
		replaceAll := false
		if len(call.Arguments) >= 4 {
			if opts, ok := call.Arguments[3].Export().(map[string]interface{}); ok {
				if v, ok := opts["all"]; ok {
					if b, ok := v.(bool); ok {
						replaceAll = b
					}
				}
			}
		}

		data, err := os.ReadFile(path)
		if err != nil {
			logErr(vm, "fs.patch", err)
		}
		content := string(data)

		count := strings.Count(content, oldText)
		if count == 0 {
			Throw(vm, "fs.patch: oldText not found in file")
		}
		if count > 1 && !replaceAll {
			Throwf(vm, "fs.patch: oldText found %d times (must be unique, or pass {all:true})", count)
		}

		n := 1
		if replaceAll {
			n = -1
		}
		result := strings.Replace(content, oldText, newText, n)
		if err := os.WriteFile(path, []byte(result), 0644); err != nil {
			logErr(vm, "fs.patch", err)
		}
		return goja.Undefined()
	})

	// fs.append(path, content) — append content to a file
	fs.Set("append", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "fs.append requires path and content arguments")
		}
		path, err := sanitize(call.Arguments[0].String())
		if err != nil {
			logErr(vm, "fs.append", err)
		}

		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			logErr(vm, "fs.append", err)
		}

		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			logErr(vm, "fs.append", err)
		}
		defer f.Close()

		content := call.Arguments[1].String()
		if _, err := f.WriteString(content); err != nil {
			logErr(vm, "fs.append", err)
		}
		return goja.Undefined()
	})

	// fs.exists(path) → boolean
	fs.Set("exists", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "fs.exists requires a path argument")
		}
		path, err := sanitize(call.Arguments[0].String())
		if err != nil {
			return vm.ToValue(false)
		}
		_, err = os.Stat(path)
		return vm.ToValue(err == nil)
	})

	// fs.stat(path) → {size, isDir, modified}
	fs.Set("stat", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "fs.stat requires a path argument")
		}
		path, err := sanitize(call.Arguments[0].String())
		if err != nil {
			logErr(vm, "fs.stat", err)
		}
		check(path, "fs.stat")
		info, err := os.Stat(path)
		if err != nil {
			logErr(vm, "fs.stat", err)
		}
		obj := vm.NewObject()
		obj.Set("size", info.Size())
		obj.Set("isDir", info.IsDir())
		obj.Set("modified", info.ModTime().Unix())
		return obj
	})

	// fs.readLines(path, start, end) → string
	// Read lines from start to end (1-indexed, inclusive).
	// Returns the selected lines joined by newlines.
	fs.Set("readLines", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 3 {
			Throw(vm, "fs.readLines requires path, start, and end arguments")
		}
		path, err := sanitize(call.Arguments[0].String())
		if err != nil {
			logErr(vm, "fs.readLines", err)
		}
		check(path, "fs.readLines")
		start := int(call.Arguments[1].ToInteger())
		end := int(call.Arguments[2].ToInteger())

		if start < 1 {
			start = 1
		}
		if end < start {
			Throw(vm, "fs.readLines: end must be >= start")
		}

		f, err := os.Open(path)
		if err != nil {
			logErr(vm, "fs.readLines", err)
		}
		defer f.Close()

		var selected []string
		scanner := newLargeScanner(f)

		lineNum := 1
		for scanner.Scan() {
			if lineNum >= start && lineNum <= end {
				selected = append(selected, scanner.Text())
			}
			if lineNum > end {
				break
			}
			lineNum++
		}
		if err := scanner.Err(); err != nil {
			Throwf(vm, "fs.readLines error: %v", err)
		}

		return vm.ToValue(strings.Join(selected, "\n"))
	})

	// fs.grep(path, pattern, opts?) → [{line, num}]
	// Search within a file for lines matching the pattern.
	// Default: substring match. Pass {regex: true} for regular expressions.
	fs.Set("grep", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "fs.grep requires path and pattern arguments")
		}
		path, err := sanitize(call.Arguments[0].String())
		if err != nil {
			logErr(vm, "fs.grep", err)
		}
		check(path, "fs.grep")
		pattern := call.Arguments[1].String()

		// Check for {regex: true} option
		var re *regexp.Regexp
		if len(call.Arguments) >= 3 {
			if opts, ok := call.Arguments[2].Export().(map[string]interface{}); ok {
				if v, ok := opts["regex"]; ok {
					if b, ok := v.(bool); ok && b {
						var compErr error
						re, compErr = regexp.Compile(pattern)
						if compErr != nil {
							logErr(vm, "fs.grep", compErr)
						}
					}
				}
			}
		}

		f, err := os.Open(path)
		if err != nil {
			logErr(vm, "fs.grep", err)
		}
		defer f.Close()

		var matches []interface{}
		scanner := newLargeScanner(f)

		lineNum := 1
		for scanner.Scan() {
			line := scanner.Text()
			matched := false
			if re != nil {
				matched = re.MatchString(line)
			} else {
				matched = strings.Contains(line, pattern)
			}
			if matched {
				obj := vm.NewObject()
				obj.Set("line", line)
				obj.Set("num", lineNum)
				matches = append(matches, obj)
			}
			lineNum++
		}
		if err := scanner.Err(); err != nil {
			Throwf(vm, "fs.grep error: %v", err)
		}

		if matches == nil {
			matches = []interface{}{}
		}
		return vm.ToValue(matches)
	})

	// fs.find(dir, pattern) → string[]
	// Recursively walk dir and return paths whose filenames match the glob pattern.
	// Pattern supports * and ? wildcards (filepath.Match semantics).
	// Example: fs.find('.', '*.go'), fs.find('src', '*.vue')
	fs.Set("find", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "fs.find requires dir and pattern arguments")
		}
		dir, err := sanitize(call.Arguments[0].String())
		if err != nil {
			logErr(vm, "fs.find", err)
		}
		check(dir, "fs.find")
		pattern := call.Arguments[1].String()

		var results []interface{}
		walkErr := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil // skip unreadable entries
			}
			matched, _ := filepath.Match(pattern, d.Name())
			if matched {
				// Return workspace-relative path
				rel, relErr := filepath.Rel(workspace, p)
				if relErr != nil {
					rel = p
				}
				results = append(results, rel)
			}
			return nil
		})
		if walkErr != nil {
			logErr(vm, "fs.find", walkErr)
		}
		if results == nil {
			results = []interface{}{}
		}
		return vm.ToValue(results)
	})

	// fs.mkdir(path) — create directory (and parents)
	fs.Set("mkdir", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "fs.mkdir requires a path argument")
		}
		path, err := sanitize(call.Arguments[0].String())
		if err != nil {
			logErr(vm, "fs.mkdir", err)
		}
		if err := os.MkdirAll(path, 0755); err != nil {
			logErr(vm, "fs.mkdir", err)
		}
		return goja.Undefined()
	})

	// fs.copy(src, dest) — copy a file
	fs.Set("copy", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "fs.copy requires src and dest arguments")
		}
		src, err := sanitize(call.Arguments[0].String())
		if err != nil {
			logErr(vm, "fs.copy", err)
		}
		dest, err := sanitize(call.Arguments[1].String())
		if err != nil {
			logErr(vm, "fs.copy", err)
		}
		check(src, "fs.copy")

		data, err := os.ReadFile(src)
		if err != nil {
			logErr(vm, "fs.copy", err)
		}

		dir := filepath.Dir(dest)
		if err := os.MkdirAll(dir, 0755); err != nil {
			logErr(vm, "fs.copy", err)
		}
		if err := os.WriteFile(dest, data, 0644); err != nil {
			logErr(vm, "fs.copy", err)
		}
		return goja.Undefined()
	})

	// fs.move(src, dest) — move/rename a file or directory
	fs.Set("move", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "fs.move requires src and dest arguments")
		}
		src, err := sanitize(call.Arguments[0].String())
		if err != nil {
			logErr(vm, "fs.move", err)
		}
		dest, err := sanitize(call.Arguments[1].String())
		if err != nil {
			logErr(vm, "fs.move", err)
		}
		check(src, "fs.move")

		dir := filepath.Dir(dest)
		if err := os.MkdirAll(dir, 0755); err != nil {
			logErr(vm, "fs.move", err)
		}
		if err := os.Rename(src, dest); err != nil {
			logErr(vm, "fs.move", err)
		}
		return goja.Undefined()
	})

	// fs.lineCount(path) → number
	// Count the number of lines in a file without loading all content into memory.
	fs.Set("lineCount", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "fs.lineCount requires a path argument")
		}
		path, err := sanitize(call.Arguments[0].String())
		if err != nil {
			logErr(vm, "fs.lineCount", err)
		}
		check(path, "fs.lineCount")
		f, err := os.Open(path)
		if err != nil {
			logErr(vm, "fs.lineCount", err)
		}
		defer f.Close()

		count := 0
		scanner := newLargeScanner(f)
		for scanner.Scan() {
			count++
		}
		if err := scanner.Err(); err != nil {
			logErr(vm, "fs.lineCount", err)
		}
		return vm.ToValue(count)
	})

	vm.Set(NameFS, fs)
}

