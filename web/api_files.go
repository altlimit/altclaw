package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"altclaw.ai/internal/search"
	"github.com/altlimit/restruct"
)

// Upload handles file uploads to the workspace.
func (a *Api) Upload(w http.ResponseWriter, r *http.Request) {
	workspace := a.server.store.Workspace().Path
	// 32 MB max
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "parse error: "+err.Error(), http.StatusBadRequest)
		return
	}
	targetDir := r.FormValue("path")
	absDir, err := checkPathSafe(workspace, targetDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.MkdirAll(absDir, 0755); err != nil {
		http.Error(w, "mkdir: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var uploaded []string
	for _, fHeaders := range r.MultipartForm.File {
		for _, fh := range fHeaders {
			name := filepath.Base(fh.Filename)
			if name == "" || name == "." || name == ".." {
				continue
			}
			dest := filepath.Join(absDir, name)
			src, err := fh.Open()
			if err != nil {
				continue
			}
			data, err := io.ReadAll(src)
			src.Close()
			if err != nil {
				continue
			}
			if err := os.WriteFile(dest, data, 0644); err != nil {
				continue
			}
			if targetDir != "" {
				uploaded = append(uploaded, targetDir+"/"+name)
			} else {
				uploaded = append(uploaded, name)
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"uploaded": uploaded})
}

// Search handles global workspace text search.
func (a *Api) Search(w http.ResponseWriter, r *http.Request) any {
	workspace := a.server.store.Workspace().Path
	query := r.URL.Query().Get("q")
	contentParam := r.URL.Query().Get("content")
	includeContent := true
	if contentParam == "false" || contentParam == "0" {
		includeContent = false
	}

	results, _ := search.Workspace(workspace, query, includeContent)
	return map[string]any{"results": results}
}

// FileEntry represents a file/directory in the workspace listing.
type FileEntry struct {
	Name  string `json:"name"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"`
}

// Files handles workspace file browsing.
func (a *Api) Files(r *http.Request) any {
	workspace := a.server.store.Workspace().Path
	relPath := r.URL.Query().Get("path")

	absPath, err := checkPathSafe(workspace, relPath)
	if err != nil {
		return restruct.Error{Status: http.StatusBadRequest, Err: err}
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return restruct.Error{Status: http.StatusNotFound, Err: err}
	}

	if info.IsDir() {
		entries, err := os.ReadDir(absPath)
		if err != nil {
			return restruct.Error{Status: http.StatusInternalServerError, Err: err}
		}
		var files []FileEntry
		for _, e := range entries {
			fe := FileEntry{Name: e.Name(), IsDir: e.IsDir()}
			if !e.IsDir() {
				if fi, err := e.Info(); err == nil {
					fe.Size = fi.Size()
				}
			}
			files = append(files, fe)
		}
		return map[string]any{"entries": files}
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	return map[string]any{"content": string(data), "name": info.Name(), "size": info.Size()}
}

// Download serves a workspace file for download/preview.
func (a *Api) Download(w http.ResponseWriter, r *http.Request) {
	workspace := a.server.store.Workspace().Path
	relPath := r.URL.Query().Get("path")

	absPath, err := checkPathSafe(workspace, relPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	http.ServeFile(w, r, absPath)
}

// Save handles saving file content from the web editor.
func (a *Api) Save(req struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}) any {
	workspace := a.server.store.Workspace().Path

	absPath, err := checkPathSafe(workspace, req.Path)
	if err != nil {
		return restruct.Error{Status: http.StatusBadRequest, Message: err.Error()}
	}

	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}

	if err := os.WriteFile(absPath, []byte(req.Content), 0644); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	return map[string]string{"status": "saved"}
}

// DeleteFile removes a file or directory from the workspace.
func (a *Api) DeleteFile(req struct {
	Path string `json:"path"`
}) any {
	workspace := a.server.store.Workspace().Path
	if req.Path == "" {
		return restruct.Error{Status: http.StatusBadRequest, Message: "invalid path"}
	}
	absPath, err := checkPathSafe(workspace, req.Path)
	if err != nil {
		return restruct.Error{Status: http.StatusBadRequest, Message: err.Error()}
	}

	realWorkspace, _ := filepath.EvalSymlinks(workspace)
	if realWorkspace == "" {
		realWorkspace = workspace
	}
	realPath, _ := filepath.EvalSymlinks(absPath)
	if realPath == "" {
		realPath = absPath
	}
	if realWorkspace == realPath {
		return restruct.Error{Status: http.StatusBadRequest, Message: "cannot delete workspace root"}
	}
	if err := os.RemoveAll(absPath); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	return map[string]string{"status": "deleted"}
}

// RenameFile renames/moves a file or directory within the workspace.
func (a *Api) RenameFile(req struct {
	OldPath string `json:"old_path"`
	NewPath string `json:"new_path"`
}) any {
	workspace := a.server.store.Workspace().Path
	if req.OldPath == "" || req.NewPath == "" {
		return restruct.Error{Status: http.StatusBadRequest, Message: "invalid path"}
	}
	oldAbs, err := checkPathSafe(workspace, req.OldPath)
	if err != nil {
		return restruct.Error{Status: http.StatusBadRequest, Message: err.Error()}
	}
	newAbs, err := checkPathSafe(workspace, req.NewPath)
	if err != nil {
		return restruct.Error{Status: http.StatusBadRequest, Message: err.Error()}
	}
	if err := os.MkdirAll(filepath.Dir(newAbs), 0755); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	if err := os.Rename(oldAbs, newAbs); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	return map[string]string{"status": "renamed"}
}

// checkPathSafe evaluates symlinks to ensure the resolved path stays within the workspace bounds.
func checkPathSafe(workspace, target string) (string, error) {

	absDir := filepath.Join(workspace, target)
	realPath, err := filepath.EvalSymlinks(absDir)
	if err != nil {
		if os.IsNotExist(err) {
			parent := filepath.Dir(absDir)
			realParent, err := filepath.EvalSymlinks(parent)
			if err != nil && !os.IsNotExist(err) {
				return "", fmt.Errorf("invalid path: %v", err)
			}
			if err == nil {
				realPath = filepath.Join(realParent, filepath.Base(absDir))
			} else {
				realPath = absDir
			}
		} else {
			return "", fmt.Errorf("invalid path: %v", err)
		}
	}

	realWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		realWorkspace = workspace
	}

	rel, err := filepath.Rel(realWorkspace, realPath)
	if err != nil {
		return "", fmt.Errorf("invalid path")
	}
	if strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path escapes workspace")
	}
	return realPath, nil
}
