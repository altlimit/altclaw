package bridge

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dop251/goja"
)

// RegisterZip adds the zip namespace to the runtime.
// All paths are workspace-jailed.
//
//	zip.create(files, output)  → void
//	zip.extract(archive, dest) → void
//	zip.list(archive)          → [{name, size, compressed, isDir}]
func RegisterZip(vm *goja.Runtime, workspace string) {
	zipObj := vm.NewObject()

	safe := func(op, path string) string {
		full, err := SanitizePath(workspace, path)
		if err != nil {
			Throwf(vm, "%s: %s", op, err)
		}
		return full
	}

	isTarGz := func(path string) bool {
		lower := strings.ToLower(path)
		return strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz")
	}

	// zip.create(files, output) — create archive from file/dir list
	zipObj.Set("create", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "zip.create requires files array and output path")
		}
		filesVal := call.Arguments[0].Export()
		outputPath := safe("zip.create", call.Arguments[1].String())

		var files []string
		switch v := filesVal.(type) {
		case []interface{}:
			for _, f := range v {
				files = append(files, fmt.Sprint(f))
			}
		case string:
			files = []string{v}
		default:
			Throw(vm, "zip.create: first argument must be an array of file paths")
		}

		// Resolve all paths
		resolved := make([]string, len(files))
		for i, f := range files {
			resolved[i] = safe("zip.create", f)
		}

		if isTarGz(outputPath) {
			if err := createTarGz(workspace, resolved, outputPath); err != nil {
				logErr(vm, "zip.create", err)
			}
		} else {
			if err := createZip(workspace, resolved, outputPath); err != nil {
				logErr(vm, "zip.create", err)
			}
		}
		return goja.Undefined()
	})

	// zip.extract(archive, dest) — extract archive to directory
	zipObj.Set("extract", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			Throw(vm, "zip.extract requires archive path and destination directory")
		}
		archivePath := safe("zip.extract", call.Arguments[0].String())
		destDir := safe("zip.extract", call.Arguments[1].String())

		if isTarGz(archivePath) {
			if err := extractTarGz(archivePath, destDir, workspace); err != nil {
				logErr(vm, "zip.extract", err)
			}
		} else {
			if err := extractZip(archivePath, destDir, workspace); err != nil {
				logErr(vm, "zip.extract", err)
			}
		}
		return goja.Undefined()
	})

	// zip.list(archive) → [{name, size, compressed, isDir}]
	zipObj.Set("list", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 1 {
			Throw(vm, "zip.list requires an archive path")
		}
		archivePath := safe("zip.list", call.Arguments[0].String())

		if isTarGz(archivePath) {
			entries, err := listTarGz(archivePath)
			if err != nil {
				logErr(vm, "zip.list", err)
			}
			return vm.ToValue(entries)
		}

		r, err := zip.OpenReader(archivePath)
		if err != nil {
			logErr(vm, "zip.list", err)
		}
		defer r.Close()

		var entries []map[string]interface{}
		for _, f := range r.File {
			entries = append(entries, map[string]interface{}{
				"name":       f.Name,
				"size":       f.UncompressedSize64,
				"compressed": f.CompressedSize64,
				"isDir":      f.FileInfo().IsDir(),
			})
		}
		return vm.ToValue(entries)
	})

	vm.Set(NameZip, zipObj)
}

func createZip(workspace string, files []string, output string) error {
	f, err := os.Create(output)
	if err != nil {
		return err
	}
	defer f.Close()

	w := zip.NewWriter(f)
	defer w.Close()

	for _, path := range files {
		err := filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(workspace, filePath)
			if err != nil {
				return err
			}

			if info.IsDir() {
				_, err := w.Create(rel + "/")
				return err
			}

			fw, err := w.Create(rel)
			if err != nil {
				return err
			}
			src, err := os.Open(filePath)
			if err != nil {
				return err
			}
			defer src.Close()
			_, err = io.Copy(fw, src)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func extractZip(archivePath, destDir, workspace string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		target := filepath.Join(destDir, f.Name)
		// Jail check
		rel, err := filepath.Rel(workspace, target)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("path escapes workspace: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			os.MkdirAll(target, 0755)
			continue
		}

		os.MkdirAll(filepath.Dir(target), 0755)
		dst, err := os.Create(target)
		if err != nil {
			return err
		}

		src, err := f.Open()
		if err != nil {
			dst.Close()
			return err
		}
		_, err = io.Copy(dst, src)
		src.Close()
		dst.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func createTarGz(workspace string, files []string, output string) error {
	f, err := os.Create(output)
	if err != nil {
		return err
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	defer gz.Close()

	tw := tar.NewWriter(gz)
	defer tw.Close()

	for _, path := range files {
		err := filepath.Walk(path, func(filePath string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(workspace, filePath)
			if err != nil {
				return err
			}

			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = rel

			if err := tw.WriteHeader(header); err != nil {
				return err
			}

			if info.IsDir() {
				return nil
			}

			src, err := os.Open(filePath)
			if err != nil {
				return err
			}
			defer src.Close()
			_, err = io.Copy(tw, src)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func extractTarGz(archivePath, destDir, workspace string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destDir, header.Name)
		rel, err := filepath.Rel(workspace, target)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("path escapes workspace: %s", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			os.MkdirAll(target, 0755)
		case tar.TypeReg:
			os.MkdirAll(filepath.Dir(target), 0755)
			dst, err := os.Create(target)
			if err != nil {
				return err
			}
			_, err = io.Copy(dst, tr)
			dst.Close()
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func listTarGz(archivePath string) ([]map[string]interface{}, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	var entries []map[string]interface{}
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		entries = append(entries, map[string]interface{}{
			"name":  header.Name,
			"size":  header.Size,
			"isDir": header.Typeflag == tar.TypeDir,
		})
	}
	return entries, nil
}
