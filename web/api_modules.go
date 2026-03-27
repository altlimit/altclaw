package web

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/altlimit/restruct"
)

// moduleEntry describes one installed module visible to the UI.
type moduleEntry struct {
	ID      string `json:"id"`      // slug (folder name)
	Scope   string `json:"scope"`   // "workspace" | "user"
	Version string `json:"version"` // from package.json, empty if absent
}

// Modules lists all installed modules from workspace and user module dirs.
func (a *Api) Modules() any {
	ws := a.server.store.Workspace()
	wsDir, userDir := a.server.store.ModuleDirs(ws.ID)
	var entries []moduleEntry
	entries = append(entries, scanModules(wsDir, "workspace")...)
	entries = append(entries, scanModules(userDir, "user")...)
	if entries == nil {
		return []moduleEntry{}
	}
	return entries
}

// scanModules scans a directory for module folders.
// Modules are expected to be direct subdirectories of baseDir.
func scanModules(baseDir, scope string) []moduleEntry {
	var entries []moduleEntry
	dirs, err := os.ReadDir(baseDir)
	if err != nil {
		return nil
	}

	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		modPath := filepath.Join(baseDir, d.Name())
		pkgPath := filepath.Join(modPath, "package.json")
		pkgData, err := os.ReadFile(pkgPath)
		if err != nil {
			continue // Not a module or no package.json
		}

		var pkg struct {
			Version string `json:"version"`
		}
		_ = json.Unmarshal(pkgData, &pkg)

		entries = append(entries, moduleEntry{
			ID:      d.Name(),
			Scope:   scope,
			Version: pkg.Version,
		})
	}
	return entries
}

// ModuleReadme serves the README.md content of an installed module.
//
//	GET /api/module-readme?id=<slug>&scope=workspace|user
func (a *Api) ModuleReadme(r *http.Request) any {
	slug := r.URL.Query().Get("id")
	if slug == "" || strings.Contains(slug, "..") {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("id required")}
	}
	scope := r.URL.Query().Get("scope")
	wsDir, userDir := a.server.store.ModuleDirs(a.server.store.Workspace().ID)
	baseDir := userDir
	if scope == "workspace" {
		baseDir = wsDir
	}
	modDir := filepath.Join(baseDir, slug)
	// Jail check
	if !strings.HasPrefix(modDir, baseDir+string(filepath.Separator)) {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("invalid id")}
	}
	for _, name := range []string{"README.md", "readme.md", "README.txt", "readme.txt"} {
		data, err := os.ReadFile(filepath.Join(modDir, name))
		if err == nil {
			return map[string]string{"html": mdToHTML(string(data))}
		}
	}
	return map[string]string{"html": ""}
}

// InstallModule accepts a ZIP file upload and extracts it under
// {modulesDir}/{zipname-without-ext}/.
//
//	POST /api/install-module?scope=workspace
//	Body: multipart/form-data  field "file" = somename.zip
func (a *Api) InstallModule(r *http.Request) any {
	scope := r.URL.Query().Get("scope")

	wsDir, userDir := a.server.store.ModuleDirs(a.server.store.Workspace().ID)
	baseDir := userDir
	if scope == "workspace" {
		baseDir = wsDir
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		return restruct.Error{Status: http.StatusBadRequest, Err: err}
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("file field required: %w", err)}
	}
	defer file.Close()

	// Determine module folder name from zip filename (strip .zip)
	modName := strings.TrimSuffix(filepath.Base(header.Filename), ".zip")
	if modName == "" || strings.ContainsAny(modName, "/\\..") {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("invalid zip filename")}
	}

	destDir := filepath.Join(baseDir, modName)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}

	// Read zip into memory (already limited by ParseMultipartForm)
	buf, err := io.ReadAll(file)
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}
	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("invalid zip: %w", err)}
	}

	for _, f := range zr.File {
		// Strip any top-level directory that may exist in the zip
		rel := stripTopDir(f.Name)
		if rel == "" || strings.HasPrefix(rel, "..") {
			continue
		}
		target := filepath.Join(destDir, rel)
		// Jail check
		if !strings.HasPrefix(target, destDir+string(filepath.Separator)) && target != destDir {
			continue
		}
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0755)
			continue
		}
		if err := extractZipFile(f, target); err != nil {
			return restruct.Error{Status: http.StatusInternalServerError, Err: err}
		}
	}

	id := modName
	a.server.BroadcastPanel([]byte(fmt.Sprintf(`{"type":"module_updated","action":"installed","id":%q}`, id)))
	return map[string]string{"status": "installed", "id": id}
}

// InstallMarketplaceModule fetches a zip from the Hub and installs it locally.
//
//	POST /api/install-marketplace-module
//	body: {"id":"altlimit/greet","scope":"workspace","version":"1.0.0"}
func (a *Api) InstallMarketplaceModule(body struct {
	ID      string `json:"id"`
	Scope   string `json:"scope"`
	Version string `json:"version"`
}) any {
	if body.ID == "" {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("id is required")}
	}

	wsDir, userDir := a.server.store.ModuleDirs(a.server.store.Workspace().ID)
	baseDir := userDir
	if body.Scope == "workspace" {
		baseDir = wsDir
	}

	hubURL := fmt.Sprintf("%s/api/modules/%s", hubHTTPURL(), body.ID)
	if body.Version != "" {
		hubURL += "-" + body.Version
	}
	hubURL += ".zip"

	resp, err := http.Get(hubURL)
	if err != nil || resp.StatusCode != 200 {
		return restruct.Error{Status: http.StatusInternalServerError, Err: fmt.Errorf("failed to download module")}
	}
	defer resp.Body.Close()

	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}

	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("invalid zip: %w", err)}
	}

	modName := strings.ReplaceAll(body.ID, "/", "-")
	destDir := filepath.Join(baseDir, modName)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}

	for _, f := range zr.File {
		rel := stripTopDir(f.Name)
		if rel == "" || strings.HasPrefix(rel, "..") {
			continue
		}
		target := filepath.Join(destDir, rel)
		if !strings.HasPrefix(target, destDir+string(filepath.Separator)) && target != destDir {
			continue
		}
		if f.FileInfo().IsDir() {
			_ = os.MkdirAll(target, 0755)
			continue
		}
		if err := extractZipFile(f, target); err != nil {
			return restruct.Error{Status: http.StatusInternalServerError, Err: err}
		}
	}

	go a.sendTelemetry(body.ID, "install")

	a.server.BroadcastPanel([]byte(fmt.Sprintf(`{"type":"module_updated","action":"installed","id":%q}`, modName)))
	return map[string]string{"status": "installed", "id": modName}
}

// DeleteModule removes an installed module directory or file.
//
//	POST /api/delete-module  body: {"id":"altlimit/greet","scope":"workspace"}
func (a *Api) DeleteModule(body struct {
	ID    string `json:"id"`
	Scope string `json:"scope"`
}) any {
	if body.ID == "" || strings.Contains(body.ID, "..") {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("invalid id")}
	}
	wsDir, userDir := a.server.store.ModuleDirs(a.server.store.Workspace().ID)
	baseDir := userDir
	if body.Scope == "workspace" {
		baseDir = wsDir
	}
	target := filepath.Join(baseDir, body.ID)
	// Jail
	if !strings.HasPrefix(target, baseDir+string(filepath.Separator)) {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("invalid id")}
	}
	if err := os.RemoveAll(target); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}

	// Try sending a telemetry ping; if the id doesn't map to a hub slug natively, it drops 404 silently.
	go a.sendTelemetry(body.ID, "uninstall")

	a.server.BroadcastPanel([]byte(fmt.Sprintf(`{"type":"module_updated","action":"deleted","id":%q}`, body.ID)))
	return map[string]string{"status": "deleted"}
}

// InstallFolderAsModule copies a workspace folder into the module directory.
//
//	POST /api/install-folder-as-module
//	body: {"path":"myfolder","scope":"workspace"}
func (a *Api) InstallFolderAsModule(body struct {
	Path  string `json:"path"`
	Scope string `json:"scope"`
}) any {
	if body.Path == "" {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("path is required")}
	}

	workspace := a.server.store.Workspace()
	// Resolve source path within workspace jail
	srcPath := filepath.Join(workspace.Path, filepath.FromSlash(body.Path))
	rel, err := filepath.Rel(workspace.Path, srcPath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("path escapes workspace")}
	}

	info, err := os.Stat(srcPath)
	if err != nil {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("path not found: %w", err)}
	}
	if !info.IsDir() {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("path must be a directory")}
	}

	wsDir, userDir := a.server.store.ModuleDirs(workspace.ID)
	baseDir := userDir
	if body.Scope == "workspace" {
		baseDir = wsDir
	}

	// Module name = folder name (slug)
	modName := info.Name()
	destDir := filepath.Join(baseDir, modName)

	// Remove existing then copy fresh
	_ = os.RemoveAll(destDir)
	if err := copyDir(srcPath, destDir); err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: err}
	}

	a.server.BroadcastPanel([]byte(fmt.Sprintf(`{"type":"module_updated","action":"installed","id":%q}`, modName)))
	return map[string]string{"status": "installed", "id": modName}
}

// ModuleHubStatus checks the hub for ownership and update info for an installed module.
//
//	GET /api/module-hub-status?id=<slug>&scope=workspace|user
func (a *Api) ModuleHubStatus(r *http.Request) any {
	slug := r.URL.Query().Get("id")
	if slug == "" {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("id required")}
	}

	// Get app config for our public key
	cfg := a.server.store.Config()
	if cfg.ModulePublicKey == "" {
		return map[string]any{"found": false, "error": "no module key configured"}
	}

	// Call hub /modules/check
	hubURL := strings.TrimRight(hubHTTPURL(), "/")
	body, _ := json.Marshal(map[string]string{"slug": slug, "public_key": cfg.ModulePublicKey})
	req, _ := http.NewRequestWithContext(r.Context(), http.MethodPost, hubURL+"/api/modules/check", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return map[string]any{"found": false, "error": "Failed to connect to hub"}
	}
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		body, _ := io.ReadAll(resp.Body)
		slog.Error("Failed to parse hub response", "status", resp.Status, "error", err, "body", string(body))
		return map[string]any{"found": false, "error": "Something went wrong"}
	}
	return result
}

// PublishModule zips an installed module and submits it to the hub marketplace.
//
//	POST /api/publish-module  body: {"id":"my-module","scope":"workspace"}
func (a *Api) PublishModule(body struct {
	ID    string `json:"id"`
	Scope string `json:"scope"`
}) any {
	if body.ID == "" || strings.Contains(body.ID, "..") {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("invalid id")}
	}

	ws := a.server.store.Workspace()
	// Find module dir
	wsDir, userDir := a.server.store.ModuleDirs(ws.ID)
	baseDir := userDir
	if body.Scope == "workspace" {
		baseDir = wsDir
	}
	modDir := filepath.Join(baseDir, body.ID)
	if _, err := os.Stat(modDir); err != nil {
		return restruct.Error{Status: http.StatusNotFound, Err: fmt.Errorf("module not found")}
	}

	// Read package.json
	pkgData, err := os.ReadFile(filepath.Join(modDir, "package.json"))
	if err != nil {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("package.json required: %w", err)}
	}
	var pkg struct {
		Name        string   `json:"name"`
		Version     string   `json:"version"`
		Description string   `json:"description"`
		Keywords    []string `json:"keywords"`
	}
	if err := json.Unmarshal(pkgData, &pkg); err != nil {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("invalid package.json: %w", err)}
	}
	if pkg.Version == "" {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("package.json must have version")}
	}
	slug := body.ID
	if pkg.Name != "" && pkg.Name != slug {
		slug = pkg.Name
	}

	// Read README if present
	var readmeContent string
	for _, name := range []string{"README.md", "readme.md", "README.txt"} {
		if data, err := os.ReadFile(filepath.Join(modDir, name)); err == nil {
			readmeContent = string(data)
			break
		}
	}

	// Get ed25519 key
	cfg := a.server.store.Config()
	if cfg.ModulePrivateKey == "" {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("no module key configured")}
	}
	privKeyBytes, err := hex.DecodeString(cfg.ModulePrivateKey)
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: fmt.Errorf("invalid private key: %w", err)}
	}
	privKey := ed25519.PrivateKey(privKeyBytes)
	message := []byte(slug + ":" + pkg.Version)
	sig := ed25519.Sign(privKey, message)
	sigHex := hex.EncodeToString(sig)

	// Zip the module directory in memory
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	err = filepath.WalkDir(modDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(modDir, path)
		if d.IsDir() {
			return nil
		}
		w, err := zw.Create(rel)
		if err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(w, f)
		return err
	})
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: fmt.Errorf("zip error: %w", err)}
	}
	_ = zw.Close()

	// POST multipart to hub /api/modules/submit
	hubURL := strings.TrimRight(ws.TunnelHub, "/")
	if hubURL == "" {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("TunnelHub not configured in workspace settings")}
	}

	var mpBody bytes.Buffer
	mw := multipart.NewWriter(&mpBody)
	_ = mw.WriteField("slug", slug)
	_ = mw.WriteField("version", pkg.Version)
	_ = mw.WriteField("name", pkg.Name)
	_ = mw.WriteField("description", pkg.Description)
	_ = mw.WriteField("tags", strings.Join(pkg.Keywords, ","))
	_ = mw.WriteField("public_key", cfg.ModulePublicKey)
	_ = mw.WriteField("signature", sigHex)
	_ = mw.WriteField("readme_content", readmeContent)
	fw, _ := mw.CreateFormFile("file", slug+"-"+pkg.Version+".zip")
	_, _ = fw.Write(buf.Bytes())
	_ = mw.Close()

	req, _ := http.NewRequest(http.MethodPost, hubURL+"/api/modules/submit", &mpBody)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return restruct.Error{Status: http.StatusBadGateway, Err: fmt.Errorf("hub request failed: %w", err)}
	}
	defer resp.Body.Close()
	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode >= 400 {
		msg, _ := result["error"].(string)
		if msg == "" {
			msg = resp.Status
		}
		return restruct.Error{Status: resp.StatusCode, Err: fmt.Errorf("%s", msg)}
	}
	return result
}

// DeleteModuleVersion removes a specific version of a module from the hub marketplace.
//
//	POST /api/delete-module-version  body: {"id":"my-module","version":"1.0.0"}
func (a *Api) DeleteModuleVersion(body struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}) any {
	if body.ID == "" || body.Version == "" {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("id and version required")}
	}

	cfg := a.server.store.Config()
	if cfg.ModulePrivateKey == "" {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("no module key configured")}
	}

	privKeyBytes, err := hex.DecodeString(cfg.ModulePrivateKey)
	if err != nil {
		return restruct.Error{Status: http.StatusInternalServerError, Err: fmt.Errorf("invalid private key: %w", err)}
	}
	privKey := ed25519.PrivateKey(privKeyBytes)
	message := []byte("delete:" + body.ID + ":" + body.Version)
	sig := ed25519.Sign(privKey, message)
	sigHex := hex.EncodeToString(sig)

	ws := a.server.store.Workspace()
	hubURL := strings.TrimRight(ws.TunnelHub, "/")
	if hubURL == "" {
		return restruct.Error{Status: http.StatusBadRequest, Err: fmt.Errorf("TunnelHub not configured in workspace settings")}
	}

	reqBody, _ := json.Marshal(map[string]string{
		"slug":       body.ID,
		"version":    body.Version,
		"public_key": cfg.ModulePublicKey,
		"signature":  sigHex,
	})

	req, _ := http.NewRequest(http.MethodPost, hubURL+"/api/modules/delete-version", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return restruct.Error{Status: http.StatusBadGateway, Err: fmt.Errorf("hub request failed: %w", err)}
	}
	defer resp.Body.Close()
	var result map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode >= 400 {
		msg, _ := result["error"].(string)
		if msg == "" {
			msg = resp.Status
		}
		return restruct.Error{Status: resp.StatusCode, Err: fmt.Errorf("%s", msg)}
	}
	return result
}

// stripTopDir strips the top-level directory from a zip entry path.
// If the zip was created with a top-level folder, strip it.
// e.g.  "mymod/index.js" → "index.js"
//
//	"index.js"      → "index.js"
func stripTopDir(name string) string {
	idx := strings.Index(name, "/")
	if idx < 0 {
		return name
	}
	// Only strip if first component looks like a directory entry
	return name[idx+1:]
}

func extractZipFile(f *zip.File, dest string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, rc)
	return err
}

// copyFile copies a single file src → dst.
func copyFile(src, dst string) error {
	r, err := os.Open(src)
	if err != nil {
		return err
	}
	defer r.Close()
	w, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer w.Close()
	_, err = io.Copy(w, r)
	return err
}

// copyDir recursively copies a directory tree from src to dst.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFile(path, target)
	})
}

func (a *Api) sendTelemetry(slug, action string) {
	domain := os.Getenv("DOMAIN")
	if domain == "" {
		domain = "altclaw.ai"
	}
	// Slug from the installed folder might be sanitized "username-module". The hub recognizes original slugs.
	// Since installing explicitly tracks `body.ID` (which is `username/module`), we natively use that.
	// For uninstalls, `body.ID` is the local folder (`username-module`), so we rewrite it.
	hubSlug := strings.ReplaceAll(slug, "-", "/")
	hubURL := fmt.Sprintf("https://hub.%s/api/modules/%s/telemetry", domain, hubSlug)

	cfg := a.server.store.Config()
	if cfg == nil || cfg.ModulePrivateKey == "" {
		return
	}
	privBytes, err := hex.DecodeString(cfg.ModulePrivateKey)
	if err != nil || len(privBytes) != ed25519.PrivateKeySize {
		return
	}
	privKey := ed25519.PrivateKey(privBytes)
	pubKey := hex.EncodeToString(privKey.Public().(ed25519.PublicKey))

	msg := fmt.Sprintf("telemetry:%s:%s", action, hubSlug)
	sig := ed25519.Sign(privKey, []byte(msg))

	payload := map[string]string{
		"action":     action,
		"public_key": pubKey,
		"signature":  hex.EncodeToString(sig),
	}
	jd, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", hubURL, bytes.NewReader(jd))
	req.Header.Set("Content-Type", "application/json")
	resp, reqErr := http.DefaultClient.Do(req)
	if reqErr == nil {
		resp.Body.Close()
	}
}
