package bridge

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dop251/goja"
	"gocloud.dev/blob"
	"gocloud.dev/blob/memblob"
)

// setupBlobVM creates a VM with a BlobPool and pre-opens an in-memory bucket
// so tests don't need cloud credentials.
func setupBlobVM(t *testing.T) (*goja.Runtime, string, *BlobPool, *blob.Bucket) {
	t.Helper()
	vm := goja.New()
	workspace := t.TempDir()
	pool := NewBlobPool()
	t.Cleanup(func() { pool.CloseAll() })

	// Register the blob bridge
	RegisterBlob(vm, pool, nil, workspace)

	// Open an in-memory bucket and put it into the pool
	bucket := memblob.OpenBucket(nil)
	pool.Put("mem:test", bucket)

	// Expose a pre-connected handle as "b" in the JS runtime
	handle := makeBlobHandle(vm, bucket, pool, "mem:test", workspace)
	vm.Set("b", handle)

	return vm, workspace, pool, bucket
}

func TestBlob_WriteRead(t *testing.T) {
	vm, _, _, _ := setupBlobVM(t)

	val, err := vm.RunString(`
		b.write("hello.txt", "Hello, World!");
		b.read("hello.txt");
	`)
	if err != nil {
		t.Fatalf("write/read failed: %v", err)
	}
	if val.String() != "Hello, World!" {
		t.Errorf("expected 'Hello, World!', got %q", val.String())
	}
}

func TestBlob_ReadRange(t *testing.T) {
	vm, _, _, _ := setupBlobVM(t)

	val, err := vm.RunString(`
		b.write("range.txt", "ABCDEFGHIJ");
		b.read("range.txt", { offset: 3, length: 4 });
	`)
	if err != nil {
		t.Fatalf("range read failed: %v", err)
	}
	if val.String() != "DEFG" {
		t.Errorf("expected 'DEFG', got %q", val.String())
	}
}

func TestBlob_ReadBuffer(t *testing.T) {
	vm, _, _, _ := setupBlobVM(t)

	val, err := vm.RunString(`
		b.write("binary.bin", "Hello");
		var buf = b.read("binary.bin", { encoding: "buffer" });
		var view = new Uint8Array(buf);
		view.length + "|" + view[0] + "|" + view[4];
	`)
	if err != nil {
		t.Fatalf("buffer read failed: %v", err)
	}
	// "Hello" = [72, 101, 108, 108, 111]
	if val.String() != "5|72|111" {
		t.Errorf("expected '5|72|111', got %q", val.String())
	}
}

func TestBlob_ReadRangeBuffer(t *testing.T) {
	vm, _, _, _ := setupBlobVM(t)

	val, err := vm.RunString(`
		b.write("combo.bin", "ABCDEFGHIJ");
		var buf = b.read("combo.bin", { offset: 2, length: 3, encoding: "buffer" });
		var view = new Uint8Array(buf);
		view.length + "|" + String.fromCharCode(view[0], view[1], view[2]);
	`)
	if err != nil {
		t.Fatalf("range+buffer read failed: %v", err)
	}
	// offset=2, length=3 → "CDE"
	if val.String() != "3|CDE" {
		t.Errorf("expected '3|CDE', got %q", val.String())
	}
}

func TestBlob_WriteWithContentType(t *testing.T) {
	vm, _, _, _ := setupBlobVM(t)

	val, err := vm.RunString(`
		b.write("data.json", '{"key":"value"}', { contentType: "application/json" });
		var s = b.stat("data.json");
		s.contentType;
	`)
	if err != nil {
		t.Fatalf("write with content type failed: %v", err)
	}
	if val.String() != "application/json" {
		t.Errorf("expected 'application/json', got %q", val.String())
	}
}

func TestBlob_Exists(t *testing.T) {
	vm, _, _, _ := setupBlobVM(t)

	val, err := vm.RunString(`
		b.write("exists.txt", "yes");
		b.exists("exists.txt") + "|" + b.exists("nope.txt");
	`)
	if err != nil {
		t.Fatalf("exists failed: %v", err)
	}
	if val.String() != "true|false" {
		t.Errorf("expected 'true|false', got %q", val.String())
	}
}

func TestBlob_Stat(t *testing.T) {
	vm, _, _, _ := setupBlobVM(t)

	val, err := vm.RunString(`
		b.write("stat.txt", "12345");
		var s = b.stat("stat.txt");
		s.size;
	`)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if val.String() != "5" {
		t.Errorf("expected size 5, got %q", val.String())
	}
}

func TestBlob_List(t *testing.T) {
	vm, _, _, _ := setupBlobVM(t)

	val, err := vm.RunString(`
		b.write("dir/a.txt", "a");
		b.write("dir/b.txt", "b");
		b.write("other.txt", "c");
		var items = b.list("dir/");
		items.length;
	`)
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if val.String() != "2" {
		t.Errorf("expected 2 items, got %q", val.String())
	}
}

func TestBlob_Delete(t *testing.T) {
	vm, _, _, _ := setupBlobVM(t)

	val, err := vm.RunString(`
		b.write("del.txt", "bye");
		b.rm("del.txt");
		b.exists("del.txt");
	`)
	if err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if val.String() != "false" {
		t.Errorf("expected false after delete, got %q", val.String())
	}
}

func TestBlob_Copy(t *testing.T) {
	vm, _, _, _ := setupBlobVM(t)

	val, err := vm.RunString(`
		b.write("src.txt", "original");
		b.copy("dst.txt", "src.txt");
		b.read("dst.txt");
	`)
	if err != nil {
		t.Fatalf("copy failed: %v", err)
	}
	if val.String() != "original" {
		t.Errorf("expected 'original', got %q", val.String())
	}
}

func TestBlob_DownloadUpload(t *testing.T) {
	vm, workspace, _, _ := setupBlobVM(t)

	// Write a blob, download to workspace, verify file exists
	val, err := vm.RunString(`
		b.write("remote.txt", "remote content");
		var dl = b.download("remote.txt", "local.txt");
		dl.bytes;
	`)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	if val.String() != "14" {
		t.Errorf("expected 14 bytes, got %q", val.String())
	}

	// Verify local file exists
	data, err := os.ReadFile(filepath.Join(workspace, "local.txt"))
	if err != nil {
		t.Fatalf("local file not found: %v", err)
	}
	if string(data) != "remote content" {
		t.Errorf("expected 'remote content', got %q", string(data))
	}

	// Upload a local file to the bucket
	os.WriteFile(filepath.Join(workspace, "upload.txt"), []byte("upload content"), 0644)

	val, err = vm.RunString(`
		var ul = b.upload("upload.txt", "uploaded.txt");
		b.read("uploaded.txt");
	`)
	if err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	if val.String() != "upload content" {
		t.Errorf("expected 'upload content', got %q", val.String())
	}
}

func TestBlob_Transfer(t *testing.T) {
	vm, _, pool, _ := setupBlobVM(t)

	// Create a second in-memory bucket
	bucket2 := memblob.OpenBucket(nil)
	pool.Put("mem:test2", bucket2)
	handle2 := makeBlobHandle(vm, bucket2, pool, "mem:test2", t.TempDir())
	vm.Set("b2", handle2)

	val, err := vm.RunString(`
		b.write("transfer.txt", "cross-bucket data");
		blob.transfer(b, "transfer.txt", b2, "received.txt");
		b2.read("received.txt");
	`)
	if err != nil {
		t.Fatalf("transfer failed: %v", err)
	}
	if val.String() != "cross-bucket data" {
		t.Errorf("expected 'cross-bucket data', got %q", val.String())
	}
}

func TestBlob_ConnectionReuse(t *testing.T) {
	pool := NewBlobPool()
	defer pool.CloseAll()

	bucket := memblob.OpenBucket(nil)
	pool.Put("mem:reuse", bucket)

	// Get should return the same bucket
	got := pool.Get("mem:reuse")
	if got != bucket {
		t.Error("expected same bucket from pool")
	}
}

func TestBlob_ConnectionsList(t *testing.T) {
	vm, _, pool, _ := setupBlobVM(t)

	// "mem:test" was already put in by setupBlobVM
	val, err := vm.RunString(`JSON.stringify(blob.connections())`)
	if err != nil {
		t.Fatalf("connections failed: %v", err)
	}
	if val.String() != `["mem:test"]` {
		t.Errorf("expected [\"mem:test\"], got %q", val.String())
	}
	_ = pool
}

func TestBlob_AutoClose(t *testing.T) {
	pool := NewBlobPool()

	bucket := memblob.OpenBucket(nil)
	pool.Put("mem:auto", bucket)

	if len(pool.List()) != 1 {
		t.Fatalf("expected 1 connection, got %d", len(pool.List()))
	}

	pool.CloseAll()

	if len(pool.List()) != 0 {
		t.Errorf("expected 0 connections after CloseAll, got %d", len(pool.List()))
	}
}

func TestBlob_PoolIdleEviction(t *testing.T) {
	pool := NewBlobPool()
	pool.idleTimeout = 50 * time.Millisecond

	bucket := memblob.OpenBucket(nil)
	pool.Put("mem:idle", bucket)
	if len(pool.List()) != 1 {
		t.Fatalf("expected 1 connection, got %d", len(pool.List()))
	}

	time.Sleep(100 * time.Millisecond)
	pool.reap()

	if len(pool.List()) != 0 {
		t.Errorf("expected 0 connections after eviction, got %d", len(pool.List()))
	}

	pool.CloseAll()
}
