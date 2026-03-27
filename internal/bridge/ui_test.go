package bridge

import (
	"strings"
	"testing"

	"github.com/dop251/goja"
)

type testUIHandler struct {
	logs []string
}

func (h *testUIHandler) Log(msg string) {
	h.logs = append(h.logs, msg)
}

func (h *testUIHandler) Ask(question string) string { return "" }
func (h *testUIHandler) Confirm(action, label, summary string, params map[string]any) string {
	return "no"
}

func TestUI_Log(t *testing.T) {
	vm := goja.New()
	handler := &testUIHandler{}
	RegisterUI(vm, handler, t.TempDir(), nil)

	_, err := vm.RunString(`ui.log("test message")`)
	if err != nil {
		t.Fatalf("ui.log failed: %v", err)
	}

	if len(handler.logs) != 1 {
		t.Fatalf("expected 1 log, got %d", len(handler.logs))
	}
	if !strings.Contains(handler.logs[0], "test message") {
		t.Errorf("expected log containing 'test message', got %q", handler.logs[0])
	}
}
