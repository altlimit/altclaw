package bridge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dop251/goja"
)

func setupFSVM(t *testing.T) (*goja.Runtime, string) {
	t.Helper()
	vm := goja.New()
	workspace := t.TempDir()
	RegisterFS(vm, workspace, nil, nil)
	return vm, workspace
}

func TestFS_WriteAndRead(t *testing.T) {
	vm, workspace := setupFSVM(t)

	// Write
	_, err := vm.RunString(`fs.write("test.txt", "hello world")`)
	if err != nil {
		t.Fatalf("fs.write failed: %v", err)
	}

	// Verify on disk
	data, err := os.ReadFile(filepath.Join(workspace, "test.txt"))
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(data) != "hello world" {
		t.Errorf("expected 'hello world', got %q", string(data))
	}

	// Read back via bridge
	val, err := vm.RunString(`fs.read("test.txt")`)
	if err != nil {
		t.Fatalf("fs.read failed: %v", err)
	}
	if val.String() != "hello world" {
		t.Errorf("fs.read returned %q", val.String())
	}
}

func TestFS_WriteCreatesDirectories(t *testing.T) {
	vm, workspace := setupFSVM(t)

	_, err := vm.RunString(`fs.write("sub/dir/test.txt", "nested")`)
	if err != nil {
		t.Fatalf("fs.write failed: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(workspace, "sub/dir/test.txt"))
	if err != nil {
		t.Fatalf("nested file not written: %v", err)
	}
	if string(data) != "nested" {
		t.Errorf("expected 'nested', got %q", string(data))
	}
}

func TestFS_List(t *testing.T) {
	vm, workspace := setupFSVM(t)

	os.WriteFile(filepath.Join(workspace, "a.txt"), []byte("a"), 0644)
	os.WriteFile(filepath.Join(workspace, "b.txt"), []byte("b"), 0644)
	os.Mkdir(filepath.Join(workspace, "subdir"), 0755)

	val, err := vm.RunString(`JSON.stringify(fs.list(".").sort())`)
	if err != nil {
		t.Fatalf("fs.list failed: %v", err)
	}
	expected := `["a.txt","b.txt","subdir"]`
	if val.String() != expected {
		t.Errorf("expected %s, got %s", expected, val.String())
	}
}

func TestFS_PathTraversalBlocked(t *testing.T) {
	vm, _ := setupFSVM(t)

	_, err := vm.RunString(`fs.read("../../etc/passwd")`)
	if err == nil {
		t.Fatal("expected error for path traversal, got none")
	}
}

func TestFS_AbsolutePathOutsideWorkspace(t *testing.T) {
	vm, _ := setupFSVM(t)

	_, err := vm.RunString(`fs.read("/etc/passwd")`)
	if err == nil {
		t.Fatal("expected error for path outside workspace, got none")
	}
}

func TestFS_ReadMissing(t *testing.T) {
	vm, _ := setupFSVM(t)

	_, err := vm.RunString(`fs.read("nonexistent.txt")`)
	if err == nil {
		t.Fatal("expected error for missing file, got none")
	}
}
