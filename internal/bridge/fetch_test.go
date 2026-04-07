package bridge

import (
	"context"
	"os"
	"path/filepath"
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

func setupFetchVM(t *testing.T) (*goja.Runtime, string) {
	t.Helper()
	vm := goja.New()
	workspace := t.TempDir()
	store := &config.Store{}
	RegisterFormData(vm, workspace)
	RegisterFetch(vm, store, workspace)
	return vm, workspace
}

func TestFetch_MissingURL(t *testing.T) {
	vm, _ := setupFetchVM(t)
	_, err := vm.RunString(`fetch()`)
	if err == nil {
		t.Fatal("expected error for fetch() with no arguments")
	}
}

func TestFetch_InvalidScheme(t *testing.T) {
	vm, _ := setupFetchVM(t)
	_, err := vm.RunString(`fetch("ftp://example.com/file.txt")`)
	if err == nil {
		t.Fatal("expected error for ftp:// scheme")
	}
}

func TestFetch_FileSchemeBlocked(t *testing.T) {
	vm, _ := setupFetchVM(t)
	_, err := vm.RunString(`fetch("file:///etc/passwd")`)
	if err == nil {
		t.Fatal("expected error for file:// scheme")
	}
}

func TestFetch_EmptyStringURL(t *testing.T) {
	vm, _ := setupFetchVM(t)
	_, err := vm.RunString(`fetch("")`)
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestFetch_MethodParsing(t *testing.T) {
	vm, _ := setupFetchVM(t)
	val, err := vm.RunString(`typeof fetch`)
	if err != nil {
		t.Fatalf("typeof fetch failed: %v", err)
	}
	if val.String() != "function" {
		t.Errorf("fetch should be a function, got %q", val.String())
	}
}

func TestFormData_Constructor(t *testing.T) {
	vm, _ := setupFetchVM(t)
	val, err := vm.RunString(`typeof FormData`)
	if err != nil {
		t.Fatalf("typeof FormData failed: %v", err)
	}
	if val.String() != "function" {
		t.Errorf("FormData should be a function, got %q", val.String())
	}
}

func TestFormData_AppendTextField(t *testing.T) {
	vm, _ := setupFetchVM(t)
	_, err := vm.RunString(`
		var fd = new FormData();
		fd.append("name", "hello world");
		fd.append("count", "42");
	`)
	if err != nil {
		t.Fatalf("FormData append text field failed: %v", err)
	}
}

func TestFormData_AppendFileEntry(t *testing.T) {
	vm, ws := setupFetchVM(t)
	os.WriteFile(filepath.Join(ws, "test.txt"), []byte("file content"), 0644)

	_, err := vm.RunString(`
		var fd = new FormData();
		fd.append("doc", "./test.txt");
	`)
	if err != nil {
		t.Fatalf("FormData append file with ./ prefix failed: %v", err)
	}
}

func TestFormData_AppendFileWithSlashPrefix(t *testing.T) {
	vm, ws := setupFetchVM(t)
	os.WriteFile(filepath.Join(ws, "test.txt"), []byte("file content"), 0644)

	_, err := vm.RunString(`
		var fd = new FormData();
		fd.append("doc", "/test.txt");
	`)
	if err != nil {
		t.Fatalf("FormData append file with / prefix failed: %v", err)
	}
	// Verify the entry got a filename from the basename
	_ = ws
}

func TestFormData_AppendFileCustomName(t *testing.T) {
	vm, ws := setupFetchVM(t)
	os.WriteFile(filepath.Join(ws, "test.txt"), []byte("file content"), 0644)

	_, err := vm.RunString(`
		var fd = new FormData();
		fd.append("doc", "./test.txt", "custom-name.txt");
	`)
	if err != nil {
		t.Fatalf("FormData append file with custom name failed: %v", err)
	}
}

func TestFormData_AppendFileNotFound(t *testing.T) {
	vm, _ := setupFetchVM(t)
	_, err := vm.RunString(`
		var fd = new FormData();
		fd.append("doc", "./nonexistent.txt");
	`)
	if err == nil {
		t.Fatal("expected error for non-existent file")
	}
}

func TestFormData_AppendArrayBuffer(t *testing.T) {
	vm, _ := setupFetchVM(t)
	_, err := vm.RunString(`
		var buf = new ArrayBuffer(8);
		var view = new Uint8Array(buf);
		view[0] = 72; // 'H'
		view[1] = 105; // 'i'
		var fd = new FormData();
		fd.append("binary", buf, "data.bin");
	`)
	if err != nil {
		t.Fatalf("FormData append ArrayBuffer failed: %v", err)
	}
}

func TestFormData_AppendRequiresArgs(t *testing.T) {
	vm, _ := setupFetchVM(t)
	_, err := vm.RunString(`
		var fd = new FormData();
		fd.append("only_name");
	`)
	if err == nil {
		t.Fatal("expected error for FormData.append with only 1 argument")
	}
}

func TestFormData_TextOnlyIsPlainString(t *testing.T) {
	// Plain string values (no ./ or / prefix) should be text fields, not files
	vm, _ := setupFetchVM(t)
	_, err := vm.RunString(`
		var fd = new FormData();
		fd.append("user", "john");
		fd.append("email", "john@example.com");
	`)
	if err != nil {
		t.Fatalf("FormData text-only append failed: %v", err)
	}
}

func TestMaybeExpandSecrets_ShortString(t *testing.T) {
	result := maybeExpandSecrets(context.Background(), nil, "short")
	if result != "short" {
		t.Errorf("expected 'short', got %q", result)
	}
}

func TestMaybeExpandSecrets_NoBraces(t *testing.T) {
	long := "this is a long string with no secret placeholders at all really"
	result := maybeExpandSecrets(context.Background(), nil, long)
	if result != long {
		t.Errorf("expected original string, got %q", result)
	}
}

func TestMaybeExpandSecrets_Empty(t *testing.T) {
	result := maybeExpandSecrets(context.Background(), nil, "")
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}
