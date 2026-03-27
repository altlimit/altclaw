package bridge

import (
	"sort"
	"strings"

	"altclaw.ai/stdlib"
	"github.com/dop251/goja"
)

// MethodProxy wraps a Goja object with a JS Proxy that intercepts unknown
// property access and throws a helpful error listing available methods with
// their signatures (pulled from the doc file).
//
// Usage:
//
//	handle := vm.NewObject()
//	handle.Set("list", func(...) { ... })
//	handle.Set("close", func(...) { ... })
//	return MethodProxy(vm, "mail", "mail client", handle)
//
// When JS code calls handle.search(...), the proxy throws:
//
//	"mail client has no method 'search'. Available:
//	  list(mailbox?, opts?) → [{uid,...}]
//	  close() → void"
func MethodProxy(vm *goja.Runtime, docName, typeName string, obj *goja.Object) goja.Value {
	// Collect method names registered on the object
	keys := obj.Keys()

	// Pull doc signatures for richer error messages
	docSigs := stdlib.DocMethodSignatures(docName)

	// Build the suggestion string once
	var sigLines []string
	for _, k := range keys {
		if sig, ok := docSigs[k]; ok {
			sigLines = append(sigLines, "  "+sig)
		} else {
			sigLines = append(sigLines, "  "+k+"()")
		}
	}
	sort.Strings(sigLines)
	suggestion := strings.Join(sigLines, "\n")

	// Create JS Proxy handler with a get trap
	handler := vm.NewObject()
	handler.Set("get", func(call goja.FunctionCall) goja.Value {
		target := call.Arguments[0].ToObject(vm)
		prop := call.Arguments[1].String()

		val := target.Get(prop)
		if val != nil && !goja.IsUndefined(val) {
			return val
		}

		// Unknown property — throw helpful error
		Throwf(vm, "%s has no method '%s'. Available methods:\n%s", typeName, prop, suggestion)
		return goja.Undefined()
	})

	// Create Proxy via JS: new Proxy(obj, handler)
	proxyConstructor := vm.Get("Proxy")
	if proxyConstructor == nil {
		// Proxy not available (shouldn't happen in modern Goja) — return raw object
		return obj
	}
	proxy, err := vm.New(proxyConstructor, obj, handler)
	if err != nil {
		// Fallback: return unwrapped object
		return obj
	}
	return proxy
}
