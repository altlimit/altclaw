package bridge

import (
	"context"
	"time"

	"github.com/altlimit/dsorm/cache"
	"github.com/dop251/goja"
)

// RegisterCache adds the cache namespace to the runtime.
// Uses the dsorm cache backend with workspace-prefixed keys.
//
//	cache.set(key, value, ttlSec?)  → void
//	cache.get(key)                  → string | null
//	cache.del(key)                  → void
//	cache.has(key)                  → boolean
//	cache.rate(key, limit, windowSec) → {allowed, remaining, resetAt}
func RegisterCache(vm *goja.Runtime, c cache.Cache, prefix string) {
	cacheObj := vm.NewObject()

	prefixKey := func(key string) string {
		return "ws:" + prefix + ":" + key
	}

	// cache.set(key, value, ttlSec?) — store a value
	cacheObj.Set("set", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "cache.set requires key and value arguments")
		}
		key := prefixKey(call.Arguments[0].String())
		value := call.Arguments[1].String()
		ttl := time.Hour // default 1 hour
		if len(call.Arguments) >= 3 {
			secs := call.Arguments[2].ToInteger()
			if secs > 0 {
				ttl = time.Duration(secs) * time.Second
			}
		}
		if err := c.Set(context.Background(), key, []byte(value), ttl); err != nil {
			logErr(vm, "cache.set", err)
		}
		return goja.Undefined()
	})

	// cache.get(key) → string | null
	cacheObj.Set("get", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "cache.get requires a key argument")
		}
		key := prefixKey(call.Arguments[0].String())
		item, err := c.Get(context.Background(), key)
		if err != nil {
			return goja.Null()
		}
		return vm.ToValue(string(item.Value))
	})

	// cache.del(key) → void
	cacheObj.Set("del", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "cache.del requires a key argument")
		}
		key := prefixKey(call.Arguments[0].String())
		if err := c.Delete(context.Background(), key); err != nil {
			logErr(vm, "cache.del", err)
		}
		return goja.Undefined()
	})

	// cache.has(key) → boolean
	cacheObj.Set("has", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "cache.has requires a key argument")
		}
		key := prefixKey(call.Arguments[0].String())
		_, err := c.Get(context.Background(), key)
		return vm.ToValue(err == nil)
	})

	// cache.rate(key, limit, windowSec) → {allowed, remaining, resetAt}
	cacheObj.Set("rate", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 3 {
			Throw(vm, "cache.rate requires key, limit, and windowSec arguments")
		}
		key := prefixKey(call.Arguments[0].String())
		limit := int(call.Arguments[1].ToInteger())
		windowSec := call.Arguments[2].ToInteger()
		if windowSec <= 0 {
			windowSec = 60
		}

		result, err := c.RateLimit(context.Background(), key, limit, time.Duration(windowSec)*time.Second)
		if err != nil {
			logErr(vm, "cache.rate", err)
		}
		obj := vm.NewObject()
		obj.Set("allowed", result.Allowed)
		obj.Set("remaining", result.Remaining)
		obj.Set("resetAt", result.ResetAt.Format(time.RFC3339))
		return obj
	})

	vm.Set(NameCache, cacheObj)
}
