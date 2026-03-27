package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dop251/goja"
)

// TestModule_List verifies mod.list() returns installed modules.
func TestModule_List(t *testing.T) {
	vm := goja.New()
	handler := &testUIHandler{}

	// Create temp module dirs
	wsDir := t.TempDir()
	userDir := t.TempDir()

	// Create a module in workspace dir
	modDir := filepath.Join(wsDir, "test-mod")
	if err := os.MkdirAll(modDir, 0755); err != nil {
		t.Fatal(err)
	}
	pkg := map[string]string{"name": "test-mod", "version": "1.2.3"}
	data, _ := json.Marshal(pkg)
	if err := os.WriteFile(filepath.Join(modDir, "package.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	RegisterUI(vm, handler, t.TempDir(), nil)
	RegisterModule(vm, func() []string { return []string{wsDir, userDir} }, handler, func() ModuleContext { return nil }, func() bool { return true })

	val, err := vm.RunString(`JSON.stringify(mod.list())`)
	if err != nil {
		t.Fatalf("mod.list() failed: %v", err)
	}

	result := val.String()
	if !strings.Contains(result, "test-mod") {
		t.Errorf("expected result to contain 'test-mod', got %s", result)
	}
	if !strings.Contains(result, "1.2.3") {
		t.Errorf("expected result to contain version '1.2.3', got %s", result)
	}
	if !strings.Contains(result, "workspace") {
		t.Errorf("expected result to contain scope 'workspace', got %s", result)
	}
}

// TestModule_Info verifies mod.info() returns module details.
func TestModule_Info(t *testing.T) {
	vm := goja.New()
	handler := &testUIHandler{}

	userDir := t.TempDir()
	modDir := filepath.Join(userDir, "greet")
	if err := os.MkdirAll(modDir, 0755); err != nil {
		t.Fatal(err)
	}
	pkg := map[string]string{"name": "greet", "version": "2.0.0", "description": "A greeting module"}
	data, _ := json.Marshal(pkg)
	os.WriteFile(filepath.Join(modDir, "package.json"), data, 0644)
	os.WriteFile(filepath.Join(modDir, "README.md"), []byte("# Greet\n\nSay hello!"), 0644)

	wsDir := t.TempDir() // empty workspace dir
	RegisterUI(vm, handler, t.TempDir(), nil)
	RegisterModule(vm, func() []string { return []string{wsDir, userDir} }, handler, func() ModuleContext { return nil }, func() bool { return true })

	val, err := vm.RunString(`JSON.stringify(mod.info("greet"))`)
	if err != nil {
		t.Fatalf("mod.info() failed: %v", err)
	}

	result := val.String()
	if !strings.Contains(result, "2.0.0") {
		t.Errorf("expected version 2.0.0, got %s", result)
	}
	if !strings.Contains(result, "A greeting module") {
		t.Errorf("expected description, got %s", result)
	}
	if !strings.Contains(result, "Say hello!") {
		t.Errorf("expected readme content, got %s", result)
	}
	if !strings.Contains(result, `"user"`) {
		t.Errorf("expected scope 'user', got %s", result)
	}
}

// TestModule_InfoNotFound verifies mod.info() returns null for non-existent modules.
func TestModule_InfoNotFound(t *testing.T) {
	vm := goja.New()
	handler := &testUIHandler{}

	wsDir := t.TempDir()
	userDir := t.TempDir()

	RegisterUI(vm, handler, t.TempDir(), nil)
	RegisterModule(vm, func() []string { return []string{wsDir, userDir} }, handler, func() ModuleContext { return nil }, func() bool { return true })

	val, err := vm.RunString(`mod.info("nonexistent") === null`)
	if err != nil {
		t.Fatalf("mod.info() failed: %v", err)
	}
	if !val.ToBoolean() {
		t.Error("expected mod.info('nonexistent') to return null")
	}
}

// TestModule_ListEmpty verifies mod.list() returns empty array when no modules exist.
func TestModule_ListEmpty(t *testing.T) {
	vm := goja.New()
	handler := &testUIHandler{}

	wsDir := t.TempDir()
	userDir := t.TempDir()

	RegisterUI(vm, handler, t.TempDir(), nil)
	RegisterModule(vm, func() []string { return []string{wsDir, userDir} }, handler, func() ModuleContext { return nil }, func() bool { return true })

	val, err := vm.RunString(`JSON.stringify(mod.list())`)
	if err != nil {
		t.Fatalf("mod.list() failed: %v", err)
	}
	if val.String() != "[]" {
		t.Errorf("expected empty array, got %s", val.String())
	}
}

// TestModule_SearchMissingArg verifies mod.search() throws when called without arguments.
func TestModule_SearchMissingArg(t *testing.T) {
	vm := goja.New()
	handler := &testUIHandler{}

	RegisterUI(vm, handler, t.TempDir(), nil)
	RegisterModule(vm, func() []string { return nil }, handler, func() ModuleContext { return nil }, func() bool { return true })

	_, err := vm.RunString(`mod.search()`)
	if err == nil {
		t.Fatal("expected error when calling mod.search() without arguments")
	}
	if !strings.Contains(err.Error(), "requires a query") {
		t.Errorf("expected 'requires a query' error, got: %v", err)
	}
}

// TestModule_InstallDenied verifies mod.install() throws when user denies.
func TestModule_InstallDenied(t *testing.T) {
	vm := goja.New()
	handler := &testUIHandler{} // Confirm returns "no" by default

	RegisterUI(vm, handler, t.TempDir(), nil)
	RegisterModule(vm, func() []string { return nil }, handler, func() ModuleContext { return nil }, func() bool { return true })

	_, err := vm.RunString(`mod.install("test-mod")`)
	if err == nil {
		t.Fatal("expected error when user rejects install")
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("expected 'rejected' error, got: %v", err)
	}
}

// TestModule_RemoveDenied verifies mod.remove() throws when user denies.
func TestModule_RemoveDenied(t *testing.T) {
	vm := goja.New()
	handler := &testUIHandler{} // Confirm returns "no" by default

	RegisterUI(vm, handler, t.TempDir(), nil)
	RegisterModule(vm, func() []string { return nil }, handler, func() ModuleContext { return nil }, func() bool { return true })

	_, err := vm.RunString(`mod.remove("test-mod")`)
	if err == nil {
		t.Fatal("expected error when user rejects remove")
	}
	if !strings.Contains(err.Error(), "rejected") {
		t.Errorf("expected 'rejected' error, got: %v", err)
	}
}
