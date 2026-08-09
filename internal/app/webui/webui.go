// Package webui serves the dependency-free Astrosferum Astrodome application.
//
// The package owns only immutable, embedded presentation assets. Authentication,
// API routes, forecast jobs, and persistence remain responsibilities of the
// composition root and the application services mounted next to this handler.
package webui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed assets
var embeddedAssets embed.FS

type asset struct {
	content     []byte
	contentType string
	etag        string
	cache       string
}

type handler struct {
	assets map[string]asset
}

const (
	englishShellPath = "/en/account/sky-conditions"
	russianShellPath = "/ru/account/sky-conditions"
)

// NewHandler returns an immutable HTTP handler for the Astrodome shell.
// It serves only GET and HEAD requests for the known embedded files. API and
// authentication endpoints deliberately are not part of this package.
func NewHandler() http.Handler {
	assets := make(map[string]asset)
	err := fs.WalkDir(embeddedAssets, "assets", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		content, readErr := embeddedAssets.ReadFile(name)
		if readErr != nil {
			return readErr
		}
		digest := sha256.Sum256(content)
		requestPath := "/" + name
		// Assets are intentionally served under stable, unhashed paths. Keep
		// them cacheable through ETags, but require revalidation so a deploy
		// cannot leave the Astrodome UI on an obsolete application contract.
		cache := "no-cache"
		if name == "assets/index.html" {
			requestPath = "/index.html"
		}
		assets[requestPath] = asset{
			content:     content,
			contentType: contentType(name),
			etag:        `"` + hex.EncodeToString(digest[:]) + `"`,
			cache:       cache,
		}
		return nil
	})
	if err != nil {
		panic("webui: embedded asset manifest: " + err.Error())
	}
	if index, ok := assets["/index.html"]; ok {
		assets[englishShellPath] = index
		assets[russianShellPath] = index
	} else {
		panic("webui: embedded index.html is missing")
	}
	return &handler{assets: assets}
}

func (h *handler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	setSecurityHeaders(response.Header())
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		response.Header().Set("Allow", "GET, HEAD")
		http.Error(response, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	if request.URL.Path == "/" || request.URL.Path == "/index.html" {
		response.Header().Set("Cache-Control", "no-store")
		http.Redirect(response, request, englishShellPath, http.StatusTemporaryRedirect)
		return
	}
	if request.URL.Path == "" || path.Clean(request.URL.Path) != request.URL.Path ||
		strings.Contains(request.URL.EscapedPath(), "%2f") || strings.Contains(request.URL.EscapedPath(), "%2F") {
		http.NotFound(response, request)
		return
	}
	item, ok := h.assets[request.URL.Path]
	if !ok {
		http.NotFound(response, request)
		return
	}
	response.Header().Set("Content-Type", item.contentType)
	if language := shellLanguage(request.URL.Path); language != "" {
		response.Header().Set("Content-Language", language)
		response.Header().Set("Link", strings.Join([]string{
			"<" + request.URL.Path + ">; rel=\"canonical\"",
			"<" + englishShellPath + ">; rel=\"alternate\"; hreflang=\"en\"",
			"<" + russianShellPath + ">; rel=\"alternate\"; hreflang=\"ru\"",
			"<" + englishShellPath + ">; rel=\"alternate\"; hreflang=\"x-default\"",
		}, ", "))
	}
	response.Header().Set("Cache-Control", item.cache)
	response.Header().Set("ETag", item.etag)
	if request.Header.Get("If-None-Match") == item.etag {
		response.WriteHeader(http.StatusNotModified)
		return
	}
	http.ServeContent(response, request, path.Base(request.URL.Path), time.Time{}, bytes.NewReader(item.content))
}

func contentType(name string) string {
	switch path.Ext(name) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	}
	if value := mime.TypeByExtension(path.Ext(name)); value != "" {
		return value
	}
	return "application/octet-stream"
}

func setSecurityHeaders(header http.Header) {
	header.Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; object-src 'none'; frame-ancestors 'none'; form-action 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; media-src 'none'; worker-src 'none'; manifest-src 'self'")
	header.Set("Cross-Origin-Opener-Policy", "same-origin")
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
	header.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), browsing-topics=()")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
	header.Set("X-Robots-Tag", "noindex, nofollow, noarchive")
}

func shellLanguage(requestPath string) string {
	switch requestPath {
	case russianShellPath:
		return "ru"
	case englishShellPath:
		return "en"
	default:
		return ""
	}
}
