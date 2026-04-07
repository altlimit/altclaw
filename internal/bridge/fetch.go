// Package bridge implements the JavaScript bridge APIs that are exposed
// to the Goja JS runtime: fetch, fs, sys, ui.
package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"altclaw.ai/internal/buildinfo"
	"altclaw.ai/internal/config"
	"altclaw.ai/internal/netx"
	"github.com/dop251/goja"
)

// fetchClient is a shared SSRF-protected HTTP client for all fetch() calls.
// Reusing a single client enables connection pooling and avoids repeated TLS handshakes.
var fetchClient = &http.Client{
	Timeout: 30 * time.Second,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           netx.SafeDialer,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

// maybeExpandSecrets runs ExpandSecrets only when the input could plausibly
// contain a {{secrets.NAME}} placeholder: at least 14 chars (the minimum
// placeholder "{{secrets.X}}") and contains the "{{" prefix. This avoids
// an expensive regex scan on large binary or data-heavy bodies.
func maybeExpandSecrets(ctx context.Context, store *config.Store, input string) string {
	if len(input) < 14 || !strings.Contains(input, "{{") {
		return input
	}
	return ExpandSecrets(ctx, store, input)
}

// ---------- FormData --------------------------------------------------

// formDataEntry represents a single field or file appended to a FormData.
type formDataEntry struct {
	name     string
	value    string // text value for fields, absolute file path for file entries
	filename string // non-empty → file entry
	data     []byte // non-nil → in-memory binary (from ArrayBuffer)
}

// formData holds an ordered list of entries, mirroring the browser FormData API.
type formData struct {
	entries   []formDataEntry
	workspace string
}

// hasFiles returns true if any entry is a file (disk or ArrayBuffer).
func (fd *formData) hasFiles() bool {
	for _, e := range fd.entries {
		if e.filename != "" || e.data != nil {
			return true
		}
	}
	return false
}

// RegisterFormData exposes the FormData constructor to the JS runtime.
//
//	var fd = new FormData();
//	fd.append("field", "text value");                       // text field
//	fd.append("file", "./report.pdf");                      // file (detected by ./ prefix)
//	fd.append("file", "/data/report.pdf");                  // file (detected by / prefix)
//	fd.append("file", "./report.pdf", "custom-name.pdf");   // file with custom filename
//	fd.append("avatar", arrayBuffer, "photo.png");          // in-memory binary
//
// File detection: values starting with "./" or "/" are treated as workspace file
// paths and streamed from disk. All other string values are plain text fields.
// If FormData contains any file entries, the body is encoded as multipart/form-data.
// If all entries are text fields, the body is encoded as application/x-www-form-urlencoded.
func RegisterFormData(vm *goja.Runtime, workspace string) {
	vm.Set("FormData", func(call goja.ConstructorCall) *goja.Object {
		fd := &formData{workspace: workspace}

		obj := call.This
		obj.Set("__formdata", fd)

		// append(name, value, filename?)
		obj.Set("append", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) < 2 {
				Throw(vm, "FormData.append requires (name, value)")
			}
			name := call.Arguments[0].String()
			val := call.Arguments[1]

			entry := formDataEntry{name: name}

			// Check if value is an ArrayBuffer
			if ab, ok := val.Export().(goja.ArrayBuffer); ok {
				entry.data = ab.Bytes()
				if len(call.Arguments) >= 3 {
					entry.filename = call.Arguments[2].String()
				} else {
					entry.filename = "blob"
				}
			} else {
				strVal := val.String()

				// Detect file paths by ./ or / prefix
				if strings.HasPrefix(strVal, "./") || strings.HasPrefix(strVal, "/") {
					// Normalize to workspace-relative: SanitizePath treats
					// absolute paths as host-absolute, so strip the leading /.
					relPath := strings.TrimPrefix(strVal, "/")
					absPath, err := SanitizePath(workspace, relPath)
					if err != nil {
						Throwf(vm, "FormData.append: invalid path %q: %s", strVal, cleanErrMsg(err))
					}
					// Validate file exists immediately
					if _, err := os.Stat(absPath); err != nil {
						Throwf(vm, "FormData.append: file not found %q", strVal)
					}
					entry.value = absPath // store resolved absolute path
					if len(call.Arguments) >= 3 {
						entry.filename = call.Arguments[2].String()
					} else {
						entry.filename = filepath.Base(absPath)
					}
				} else {
					// Plain text field
					entry.value = strVal
				}
			}

			fd.entries = append(fd.entries, entry)
			return goja.Undefined()
		})

		return nil
	})
}

// buildFormDataBody creates the appropriate body encoding from a FormData object.
// If any entry is a file, it produces a streaming multipart/form-data body via io.Pipe.
// If all entries are text fields, it produces a simple application/x-www-form-urlencoded body.
// Returns the body reader and the Content-Type header value.
func buildFormDataBody(vm *goja.Runtime, fd *formData) (io.Reader, string) {
	if !fd.hasFiles() {
		// URL-encoded: all text fields
		var parts []string
		for _, entry := range fd.entries {
			parts = append(parts, url.QueryEscape(entry.name)+"="+url.QueryEscape(entry.value))
		}
		return strings.NewReader(strings.Join(parts, "&")), "application/x-www-form-urlencoded"
	}

	// Multipart: at least one file entry
	pr, pw := io.Pipe()
	mpw := multipart.NewWriter(pw)

	go func() {
		defer pw.Close()

		for _, entry := range fd.entries {
			switch {
			case entry.data != nil:
				// In-memory binary (ArrayBuffer)
				part, err := mpw.CreateFormFile(entry.name, entry.filename)
				if err != nil {
					pw.CloseWithError(err)
					return
				}
				if _, err := part.Write(entry.data); err != nil {
					pw.CloseWithError(err)
					return
				}

			case entry.filename != "":
				// File on disk — stream it (entry.value is already an absolute path)
				f, err := os.Open(entry.value)
				if err != nil {
					pw.CloseWithError(fmt.Errorf("FormData file %q: %w", entry.name, err))
					return
				}
				part, err := mpw.CreateFormFile(entry.name, entry.filename)
				if err != nil {
					f.Close()
					pw.CloseWithError(err)
					return
				}
				if _, err := io.Copy(part, f); err != nil {
					f.Close()
					pw.CloseWithError(err)
					return
				}
				f.Close()

			default:
				// Plain text field
				if err := mpw.WriteField(entry.name, entry.value); err != nil {
					pw.CloseWithError(err)
					return
				}
			}
		}
		mpw.Close()
	}()

	return pr, mpw.FormDataContentType()
}

// ---------- fetch() ---------------------------------------------------

// RegisterFetch adds the global fetch(url, options) function to the runtime.
func RegisterFetch(vm *goja.Runtime, store *config.Store, workspace string, ctxFn ...func() context.Context) {
	vm.Set(NameFetch, func(call goja.FunctionCall) (ret goja.Value) {
		getCtx := defaultCtxFn(ctxFn)
		ctx := getCtx()

		if len(call.Arguments) < 1 {
			Throw(vm, "fetch requires a URL argument")
		}

		url := ExpandSecrets(ctx, store, call.Arguments[0].String())
		lowerURL := strings.ToLower(url)
		if !strings.HasPrefix(lowerURL, "http://") && !strings.HasPrefix(lowerURL, "https://") {
			Throw(vm, "fetch error: unsupported scheme. Only http/https are allowed.")
		}

		method := "GET"
		var body io.Reader
		headers := make(map[string]string)
		var downloadPath string
		secrets := false // opt-in: set to true to expand {{secrets.}} in body

		if len(call.Arguments) > 1 {
			opts := call.Arguments[1].ToObject(vm)
			CheckOpts(vm, "fetch", opts, "method", "body", "headers", "download", "secrets")
			if m := opts.Get("method"); m != nil && !goja.IsUndefined(m) {
				method = strings.ToUpper(m.String())
			}
			if s := opts.Get("secrets"); s != nil && !goja.IsUndefined(s) {
				secrets = s.ToBoolean()
			}
			if b := opts.Get("body"); b != nil && !goja.IsUndefined(b) {
				// Check if body is a FormData instance
				if bObj := b.ToObject(vm); bObj != nil {
					if fdVal := bObj.Get("__formdata"); fdVal != nil && !goja.IsUndefined(fdVal) {
						if fd, ok := fdVal.Export().(*formData); ok {
							bodyReader, contentType := buildFormDataBody(vm, fd)
							body = bodyReader
							headers["Content-Type"] = contentType
						}
					}
				}
				// If not FormData, treat as string body
				if body == nil {
					bStr := b.String()
					if secrets {
						bStr = maybeExpandSecrets(ctx, store, bStr)
					}
					body = strings.NewReader(bStr)
				}
			}
			if h := opts.Get("headers"); h != nil && !goja.IsUndefined(h) {
				hObj := h.ToObject(vm)
				for _, key := range hObj.Keys() {
					vStr := hObj.Get(key).String()
					headers[key] = ExpandSecrets(ctx, store, vStr)
				}
			}
			if dl := opts.Get("download"); dl != nil && !goja.IsUndefined(dl) {
				downloadPath = dl.String()
			}
		}

		if downloadPath != "" {
			absPath, err := SanitizePath(workspace, downloadPath)
			if err != nil {
				logErr(vm, "fetch", err)
			}
			downloadPath = absPath
		}

		req, err := http.NewRequest(method, url, body)
		if err != nil {
			logErr(vm, "fetch", err)
		}
		// Set sensible defaults
		req.Header.Set("User-Agent", "AltClaw/"+buildinfo.Version)
		req.Header.Set("Accept", "*/*")
		// Apply custom headers (can override defaults)
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := fetchClient.Do(req)
		if err != nil {
			logErr(vm, "fetch", err)
		}
		defer resp.Body.Close()

		if downloadPath != "" {
			dir := filepath.Dir(downloadPath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				Throwf(vm, "fetch error making dir: %v", err)
			}
			f, err := os.Create(downloadPath)
			if err != nil {
				Throwf(vm, "fetch error creating file: %v", err)
			}
			defer f.Close()

			written, err := io.Copy(f, resp.Body)
			if err != nil {
				logErr(vm, "fetch", err)
			}

			result := vm.NewObject()
			result.Set("status", resp.StatusCode)
			result.Set("statusText", resp.Status)
			result.Set("bytes", written)
			result.Set("file", downloadPath)
			return result
		}

		respBody, err := io.ReadAll(io.LimitReader(resp.Body, 32*1024*1024))
		if err != nil {
			logErr(vm, "fetch", err)
		}

		// Build the response object
		result := vm.NewObject()
		result.Set("status", resp.StatusCode)
		result.Set("statusText", resp.Status)

		// Expose response headers
		hdrs := vm.NewObject()
		for key, vals := range resp.Header {
			hdrs.Set(strings.ToLower(key), strings.Join(vals, ", "))
		}
		hdrs.Set("get", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) < 1 {
				return goja.Undefined()
			}
			key := strings.ToLower(call.Arguments[0].String())
			v := resp.Header.Get(key)
			if v == "" {
				return goja.Null()
			}
			return vm.ToValue(v)
		})
		result.Set("headers", hdrs)

		bodyStr := string(respBody)
		result.Set("text", func(call goja.FunctionCall) goja.Value {
			return vm.ToValue(bodyStr)
		})
		result.Set("json", func(call goja.FunctionCall) goja.Value {
			var parsed interface{}
			if err := json.Unmarshal([]byte(bodyStr), &parsed); err != nil {
				logErr(vm, "fetch.json", err)
			}
			return vm.ToValue(parsed)
		})

		return result
	})
}
