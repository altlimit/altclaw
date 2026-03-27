package bridge

import (
	"testing"

	"altclaw.ai/internal/config"
	"github.com/dop251/goja"
)

func TestFetchClient_SharedInstance(t *testing.T) {
	if fetchClient == nil {
		t.Fatal("fetchClient should be non-nil at package init")
	}
	if fetchClient.Timeout == 0 {
		t.Error("fetchClient should have a non-zero timeout")
	}
}

func setupFetchVM(t *testing.T) *goja.Runtime {
	t.Helper()
	vm := goja.New()
	workspace := t.TempDir()
	store := &config.Store{}
	RegisterFetch(vm, store, workspace)
	return vm
}

func TestFetch_MissingURL(t *testing.T) {
	vm := setupFetchVM(t)
	_, err := vm.RunString(`fetch()`)
	if err == nil {
		t.Fatal("expected error for fetch() with no arguments")
	}
}

func TestFetch_InvalidScheme(t *testing.T) {
	vm := setupFetchVM(t)
	_, err := vm.RunString(`fetch("ftp://example.com/file.txt")`)
	if err == nil {
		t.Fatal("expected error for ftp:// scheme")
	}
}

func TestFetch_FileSchemeBlocked(t *testing.T) {
	vm := setupFetchVM(t)
	_, err := vm.RunString(`fetch("file:///etc/passwd")`)
	if err == nil {
		t.Fatal("expected error for file:// scheme")
	}
}

func TestFetch_EmptyStringURL(t *testing.T) {
	vm := setupFetchVM(t)
	_, err := vm.RunString(`fetch("")`)
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestFetch_MethodParsing(t *testing.T) {
	// We can't actually make HTTP calls due to SSRF dialer, but we can
	// verify that the bridge is registered and callable
	vm := setupFetchVM(t)
	val, err := vm.RunString(`typeof fetch`)
	if err != nil {
		t.Fatalf("typeof fetch failed: %v", err)
	}
	if val.String() != "function" {
		t.Errorf("fetch should be a function, got %q", val.String())
	}
}
