package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed static
var files embed.FS

func embeddedRoot() fs.FS {
	root, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	return root
}

func FileSystem() http.FileSystem {
	return http.FS(embeddedRoot())
}

func SPAHandler() http.Handler {
	root := embeddedRoot()
	server := http.FileServer(http.FS(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		assetPath := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if assetPath != "" && assetPath != "." {
			file, err := root.Open(assetPath)
			if err == nil {
				info, statErr := file.Stat()
				_ = file.Close()
				if statErr == nil && !info.IsDir() {
					server.ServeHTTP(w, r)
					return
				}
			}
		}
		fallback := r.Clone(r.Context())
		urlCopy := *r.URL
		urlCopy.Path = "/"
		urlCopy.RawPath = ""
		fallback.URL = &urlCopy
		server.ServeHTTP(w, fallback)
	})
}
