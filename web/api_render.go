package web

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

var (
	mdRenderer goldmark.Markdown
	sanitizer  *bluemonday.Policy
)

func init() {
	// Goldmark with GFM (tables, strikethrough, autolinks, task lists) + hard wraps
	mdRenderer = goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(
			html.WithHardWraps(),
			html.WithUnsafe(), // we sanitize with bluemonday after
		),
	)

	// Bluemonday UGC policy as a base, then add extras
	sanitizer = bluemonday.UGCPolicy()

	// Allow class attributes on common elements (for syntax highlighting, ws-file-link, etc.)
	sanitizer.AllowAttrs("class").Globally()

	// Allow data-ws-path on anchors (workspace file links)
	sanitizer.AllowAttrs("data-ws-path").OnElements("a")

	// Allow target and rel on anchors (for opening external links in new tab)
	sanitizer.AllowAttrs("target").Matching(regexp.MustCompile(`^_blank$`)).OnElements("a")
	sanitizer.AllowAttrs("rel").Matching(regexp.MustCompile(`^noopener noreferrer$`)).OnElements("a")

	// Allow style on images (for inline sizing of ws: images)
	sanitizer.AllowAttrs("style").OnElements("img")

	// Allow title on anchors
	sanitizer.AllowAttrs("title").OnElements("a")
}

// RenderMarkdown converts markdown text to sanitized HTML.
//
//	POST /api/render-markdown  body: {"markdown":"..."}
func (a *Api) RenderMarkdown(body struct {
	Markdown string `json:"markdown"`
}) any {
	return map[string]string{"html": mdToHTML(body.Markdown)}
}

// wsLinkRe matches <a href="ws:PATH">TEXT</a> produced by goldmark.
var wsLinkRe = regexp.MustCompile(`<a href="ws:([^"]+)"[^>]*>(.*?)</a>`)

// wsImgRe matches <img src="ws:PATH" ...> produced by goldmark.
var wsImgRe = regexp.MustCompile(`<img src="ws:([^"]+)"([^>]*)>`)

// extLinkRe matches plain http/https anchors that don't already have target=.
var extLinkRe = regexp.MustCompile(`<a (href="https?://[^"]*"[^>]*)>`)

func mdToHTML(markdown string) string {
	if markdown == "" {
		return ""
	}

	var buf bytes.Buffer
	if err := mdRenderer.Convert([]byte(markdown), &buf); err != nil {
		return ""
	}

	out := buf.String()

	// 1. ws: links → workspace file link (matches frontend renderer.link)
	out = wsLinkRe.ReplaceAllStringFunc(out, func(m string) string {
		sub := wsLinkRe.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		path := sub[1]
		text := sub[2]
		if text == "" {
			text = "📄 " + path
		} else if !strings.HasPrefix(text, "📄") {
			text = "📄 " + text
		}
		return `<a class="ws-file-link" data-ws-path="` + path + `" title="Open ` + path + `">` + text + `</a>`
	})

	// 2. ws: images → served via /api/download (matches frontend renderer.image)
	out = wsImgRe.ReplaceAllStringFunc(out, func(m string) string {
		sub := wsImgRe.FindStringSubmatch(m)
		if len(sub) < 3 {
			return m
		}
		path := sub[1]
		attrs := sub[2]
		// Extract alt from attrs if present
		altRe := regexp.MustCompile(`alt="([^"]*)"`)
		alt := path
		if altMatch := altRe.FindStringSubmatch(attrs); len(altMatch) > 1 {
			alt = altMatch[1]
		}
		src := "/api/download?path=" + path
		return `<a class="ws-file-link" data-ws-path="` + path + `" title="Open ` + path + `">` +
			`<img src="` + src + `" alt="` + alt + `" style="max-width:300px;max-height:200px;border-radius:8px;cursor:pointer;" /></a>`
	})

	// 3. External http/https links → open in new tab (matches frontend renderer.link)
	out = extLinkRe.ReplaceAllStringFunc(out, func(m string) string {
		if strings.Contains(m, "target=") {
			return m
		}
		sub := extLinkRe.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m
		}
		return `<a ` + sub[1] + ` target="_blank" rel="noopener noreferrer">`
	})

	safe := sanitizer.SanitizeBytes([]byte(out))
	return string(safe)
}
