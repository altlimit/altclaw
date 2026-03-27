package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/dop251/goja"
)

// Throw panics with a proper JS Error object so that catch blocks
// can access e.message and get clean stack traces without GoError prefix.
func Throw(vm *goja.Runtime, msg string) {
	slog.Debug("bridge throw", "msg", msg)
	errCtor := vm.Get("Error")
	if errCtor == nil {
		panic(vm.NewGoError(errors.New(msg)))
	}
	errObj, err := vm.New(errCtor, vm.ToValue(msg))
	if err != nil {
		panic(vm.NewGoError(errors.New(msg)))
	}
	panic(errObj)
}

// Throwf is a convenience wrapper around Throw with fmt.Sprintf formatting.
func Throwf(vm *goja.Runtime, format string, args ...interface{}) {
	Throw(vm, fmt.Sprintf(format, args...))
}

// logErr logs the raw Go error at Debug level (for diagnostics) and throws a
// clean, prefixed message to the JS runtime — keeping internal paths/addresses
// out of JS stack traces.
// context should be a brief label like "fs.read" or "fetch".
func logErr(vm *goja.Runtime, context string, err error) {
	slog.Debug("bridge error", "op", context, "err", err)
	Throwf(vm, "%s: %s", context, cleanErrMsg(err))
}

// cleanErrMsg extracts a short, human-readable message from a Go error,
// stripping any go-internal detail (package paths, type names, etc.).
func cleanErrMsg(err error) string {
	if err == nil {
		return "unknown error"
	}
	msg := err.Error()
	// os.PathError: strip the Op and Path, keep only the innermost cause
	// "open /some/path: no such file or directory" → "no such file or directory"
	if idx := strings.LastIndex(msg, ": "); idx >= 0 {
		tail := msg[idx+2:]
		// Only use the tail if it doesn't look like a Go package path
		if !strings.Contains(tail, "/") && !strings.Contains(tail, "github.com") {
			return tail
		}
	}
	return msg
}


// CheckOpts validates that all keys on 'obj' are present in the allowed set.
// If an unknown key is found, it throws a descriptive error with a "did you mean?"
// suggestion based on Levenshtein distance.
// Use this at the top of any bridge function that accepts an options object to
// catch invalid parameters early (e.g. "text" instead of "body" in mail.send).
func CheckOpts(vm *goja.Runtime, op string, obj *goja.Object, allowed ...string) {
	allowedSet := make(map[string]bool, len(allowed))
	for _, k := range allowed {
		allowedSet[k] = true
	}
	for _, key := range obj.Keys() {
		if allowedSet[key] {
			continue
		}
		// Find closest match for "did you mean?" suggestion
		best, bestDist := "", len(key)+1
		for _, a := range allowed {
			d := levenshtein(key, a)
			if d < bestDist {
				best, bestDist = a, d
			}
		}
		hint := ""
		if bestDist <= 3 && best != "" {
			hint = fmt.Sprintf(" (did you mean %q?)", best)
		}
		Throwf(vm, "%s: unknown parameter %q%s. Valid parameters: %s",
			op, key, hint, strings.Join(allowed, ", "))
	}
}

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			ins := curr[j-1] + 1
			del := prev[j] + 1
			sub := prev[j-1] + cost
			m := ins
			if del < m {
				m = del
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// defaultCtxFn returns the first context factory if provided, otherwise returns
// a function that yields context.Background(). Used by bridge registrations
// to optionally receive a broadcast-enriched context.
func defaultCtxFn(fns []func() context.Context) func() context.Context {
	if len(fns) > 0 && fns[0] != nil {
		return fns[0]
	}
	return context.Background
}

// Stringify converts a Goja value to a human-readable string.
// Primitives (string, number, bool) use their natural string form.
// Objects and arrays are serialised with JSON.stringify so that
// ui.log(someArray) shows proper JSON instead of "[object Object]".
func Stringify(vm *goja.Runtime, val goja.Value) string {
	if val == nil || goja.IsUndefined(val) {
		return "undefined"
	}
	if goja.IsNull(val) {
		return "null"
	}

	// For objects and arrays, delegate to JS JSON.stringify so all goja-native
	// types (including slices of structs returned by bridges) are rendered correctly.
	if obj := val.ToObject(vm); obj != nil {
		class := obj.ClassName()
		if class == "Object" || class == "Array" {
			jsonFn, ok := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("stringify"))
			if ok {
				if result, err := jsonFn(goja.Undefined(), val, goja.Undefined(), vm.ToValue(2)); err == nil {
					return result.String()
				}
			}
			// Fallback: Go json.Marshal on exported value
			if data, err := json.Marshal(val.Export()); err == nil {
				return string(data)
			}
		}
	}

	return val.String()
}

// lowercase alias for internal use within bridge package
func stringify(vm *goja.Runtime, val goja.Value) string {
	return Stringify(vm, val)
}
