// Package bridge implements the JavaScript bridge APIs that are exposed
// to the Goja JS runtime: fetch, fs, sys, ui.
package bridge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
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

		if len(call.Arguments) > 1 {
			opts := call.Arguments[1].ToObject(vm)
			CheckOpts(vm, "fetch", opts, "method", "body", "headers", "download")
			if m := opts.Get("method"); m != nil && !goja.IsUndefined(m) {
				method = strings.ToUpper(m.String())
			}
			if b := opts.Get("body"); b != nil && !goja.IsUndefined(b) {
				bStr := ExpandSecrets(ctx, store, b.String())
				body = strings.NewReader(bStr)
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
