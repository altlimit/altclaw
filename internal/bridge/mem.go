package bridge

import (
	"context"
	"math"
	"strings"
	"time"

	"altclaw.ai/internal/config"
	"github.com/dop251/goja"
)

// RegisterMem adds the mem namespace to the runtime with structured memory entries.
//
// Writing:
//
//	mem.add(content, kind?, scope?)  — add memory entry. kind: "core"|"learned"|"note", default "learned".
//	                                   scope: "user" for global, omit for workspace. Returns ID.
//
// Reading:
//
//	mem.recent(days?)   — entries from last N days (default 7), newest first
//	mem.core()          — all core entries (workspace + user)
//	mem.all()           — everything, newest first
//	mem.search(query)   — keyword search across all entries
//
// Management:
//
//	mem.rm(id, scope?)      — remove entry by ID. scope: "user" for global entries.
//	mem.promote(id, scope?) — promote learned/note → core. scope: "user" for global entries.
//
// ctxFn is an optional context factory that returns a broadcast-enriched context
// so that AfterSave hooks on the Memory model can fire SSE events.
func RegisterMem(vm *goja.Runtime, store *config.Store, workspace string, ctxFn ...func() context.Context) {
	mem := vm.NewObject()
	getCtx := defaultCtxFn(ctxFn)

	validKind := func(k string) bool {
		return k == "core" || k == "learned" || k == "note"
	}

	// mem.add(content, kind?, scope?) — add memory entry
	// scope: "user" for global (cross-workspace), omit or "" for workspace-local.
	mem.Set("add", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			return vm.ToValue("")
		}
		content := call.Arguments[0].String()
		kind := "learned"
		if len(call.Arguments) >= 2 {
			kind = call.Arguments[1].String()
		}
		if !validKind(kind) {
			kind = "learned"
		}
		ns := workspace
		if len(call.Arguments) >= 3 && call.Arguments[2].String() == "user" {
			ns = ""
		}
		entry := &config.Memory{Workspace: ns, Kind: kind, Content: content}
		if err := store.AddMemory(getCtx(), entry); err != nil {
			logErr(vm, "mem.add", err)
		}
		return vm.ToValue(entry.ID)
	})

	// mem.recent(days?) — entries from last N days (default 7)
	mem.Set("recent", func(call goja.FunctionCall) goja.Value {
		days := 7
		if len(call.Arguments) >= 1 {
			days = int(call.Arguments[0].ToInteger())
			if days < 1 {
				days = 1
			}
		}
		since := time.Now().AddDate(0, 0, -days)
		return vm.ToValue(mergeEntries(store, getCtx(), workspace, since, ""))
	})

	// mem.core() — all core entries (workspace + user)
	mem.Set("core", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(mergeEntriesByKind(store, getCtx(), workspace, "core"))
	})

	// mem.all() — everything, newest first
	mem.Set("all", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(mergeEntries(store, getCtx(), workspace, time.Time{}, ""))
	})

	// mem.search(query) — keyword search across all entries
	mem.Set("search", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "mem.search requires a query string argument")
		}
		query := call.Arguments[0].String()
		return vm.ToValue(mergeEntries(store, getCtx(), workspace, time.Time{}, query))
	})

	// mem.rm(id, scope?) — remove entry by ID
	// scope: "user" for global entries, omit for workspace-local.
	mem.Set("rm", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "mem.rm requires an entry ID")
		}
		id := call.Arguments[0].ToInteger()
		ns := workspace
		if len(call.Arguments) >= 2 && call.Arguments[1].String() == "user" {
			ns = ""
		}
		if err := store.DeleteMemory(getCtx(), ns, id); err != nil {
			Throwf(vm, "mem.rm: entry %d not found", id)
		}
		return vm.ToValue("removed")
	})

	// mem.promote(id, scope?) — promote learned/note → core
	// scope: "user" for global entries, omit for workspace-local.
	mem.Set("promote", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "mem.promote requires an entry ID")
		}
		id := call.Arguments[0].ToInteger()
		ns := workspace
		if len(call.Arguments) >= 2 && call.Arguments[1].String() == "user" {
			ns = ""
		}
		if err := store.PromoteMemory(getCtx(), ns, id); err != nil {
			Throwf(vm, "mem.promote: entry %d not found", id)
		}
		return vm.ToValue("promoted to core")
	})

	vm.Set(NameMem, mem)
}

// mergeEntries returns entries from both workspace and user scopes, filtered by time and query.
func mergeEntries(store *config.Store, ctx context.Context, workspace string, since time.Time, query string) []map[string]interface{} {
	var wsEntries, userEntries []*config.Memory

	if since.IsZero() {
		wsEntries, _ = store.ListMemoryEntries(ctx, workspace)
		userEntries, _ = store.ListMemoryEntries(ctx, "")
	} else {
		wsEntries, _ = store.ListRecentMemoryEntries(ctx, workspace, since)
		userEntries, _ = store.ListRecentMemoryEntries(ctx, "", since)
	}

	var results []map[string]interface{}
	queryWords := tokenize(query)

	addEntries := func(entries []*config.Memory, scope string) {
		for _, e := range entries {
			if len(queryWords) > 0 {
				score := overlapScore(queryWords, tokenize(e.Content))
				if score < 0.1 {
					continue
				}
			}
			m := map[string]interface{}{
				"id":      e.ID,
				"kind":    e.Kind,
				"content": e.Content,
				"age":     config.FormatAge(time.Since(e.CreatedAt)),
			}
			if scope != "" {
				m["scope"] = scope
			}
			results = append(results, m)
		}
	}

	if workspace != "" {
		addEntries(wsEntries, "")
	}
	addEntries(userEntries, "user")
	return results
}

// mergeEntriesByKind returns entries of a specific kind from both scopes.
func mergeEntriesByKind(store *config.Store, ctx context.Context, workspace, kind string) []map[string]interface{} {
	var results []map[string]interface{}

	addEntries := func(entries []*config.Memory, scope string) {
		for _, e := range entries {
			m := map[string]interface{}{
				"id":      e.ID,
				"kind":    e.Kind,
				"content": e.Content,
			}
			if scope != "" {
				m["scope"] = scope
			}
			results = append(results, m)
		}
	}

	if workspace != "" {
		wsEntries, _ := store.ListMemoryEntriesByKind(ctx, workspace, kind)
		addEntries(wsEntries, "")
	}
	userEntries, _ := store.ListMemoryEntriesByKind(ctx, "", kind)
	addEntries(userEntries, "user")
	return results
}

// tokenize splits text into lowercase words.
func tokenize(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.ToLower(text)
	words := strings.FieldsFunc(text, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	})
	seen := make(map[string]bool)
	var result []string
	for _, w := range words {
		if len(w) > 1 && !seen[w] {
			seen[w] = true
			result = append(result, w)
		}
	}
	return result
}

// overlapScore calculates a cosine-like similarity between word sets.
func overlapScore(queryWords, corpusWords []string) float64 {
	corpusSet := make(map[string]bool, len(corpusWords))
	for _, w := range corpusWords {
		corpusSet[w] = true
	}
	matches := 0
	for _, qw := range queryWords {
		if corpusSet[qw] {
			matches++
		}
	}
	if matches == 0 {
		return 0
	}
	return float64(matches) / math.Sqrt(float64(len(queryWords))*float64(len(corpusWords)))
}
