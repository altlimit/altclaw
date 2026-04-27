package bridge

import (
	"context"
	"fmt"

	"github.com/dop251/goja"
)

// ConnManager is the interface the conn bridge uses to manage connections.
// Implemented by connmgr.Manager — the List() return type is interface{}
// to avoid import cycles between bridge and connmgr packages.
type ConnManager interface {
	Add(ctx context.Context, chatID int64, connType, url, handler string, headers map[string]string, reconnect bool) (int64, error)
	Remove(ctx context.Context, id int64) error
	Send(id int64, data string) error
	List() any // returns []connmgr.ConnInfo — serialized to JS via goja
}

// RegisterConn registers the conn bridge on the Goja VM.
// conn.open(url, handler, opts?) → id
// conn.list() → [{id, type, url, handler, status, ...}]
// conn.send(id, data) → void
// conn.close(id) → void
func RegisterConn(vm *goja.Runtime, mgr ConnManager, workspace string, chatIDFn func() int64, broadcastCtx func() context.Context) {
	if mgr == nil {
		return
	}

	obj := vm.NewObject()

	// conn.open(url, handler, opts?) → connection ID (string)
	obj.Set("open", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "conn.open requires url and handler arguments")
		}

		url := call.Arguments[0].String()
		handler := call.Arguments[1].String()

		connType := ""
		headers := map[string]string{}
		reconnect := true

		if len(call.Arguments) > 2 && !goja.IsUndefined(call.Arguments[2]) && !goja.IsNull(call.Arguments[2]) {
			optsObj := call.Arguments[2].ToObject(vm)
			if v := optsObj.Get("type"); v != nil && !goja.IsUndefined(v) {
				connType = v.String()
			}
			if v := optsObj.Get("reconnect"); v != nil && !goja.IsUndefined(v) {
				reconnect = v.ToBoolean()
			}
			if v := optsObj.Get("headers"); v != nil && !goja.IsUndefined(v) {
				hObj := v.ToObject(vm)
				for _, key := range hObj.Keys() {
					headers[key] = hObj.Get(key).String()
				}
			}
		}

		chatID := int64(0)
		if chatIDFn != nil {
			chatID = chatIDFn()
		}

		ctx := broadcastCtx()
		id, err := mgr.Add(ctx, chatID, connType, url, handler, headers, reconnect)
		if err != nil {
			Throw(vm, "conn.open: "+err.Error())
		}

		return vm.ToValue(fmt.Sprintf("%d", id))
	})

	// conn.list() → array of connection info objects
	obj.Set("list", func(call goja.FunctionCall) goja.Value {
		list := mgr.List()
		return vm.ToValue(list)
	})

	// conn.send(id, data) → void
	obj.Set("send", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "conn.send requires id and data arguments")
		}

		id := call.Arguments[0].ToInteger()
		data := call.Arguments[1]

		dataStr := Stringify(vm, data)

		if err := mgr.Send(id, dataStr); err != nil {
			Throw(vm, "conn.send: "+err.Error())
		}

		return goja.Undefined()
	})

	// conn.close(id) → void
	obj.Set("close", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "conn.close requires an id argument")
		}

		id := call.Arguments[0].ToInteger()
		ctx := broadcastCtx()
		if err := mgr.Remove(ctx, id); err != nil {
			Throw(vm, "conn.close: "+err.Error())
		}

		return goja.Undefined()
	})

	vm.Set(NameConn, obj)
}
