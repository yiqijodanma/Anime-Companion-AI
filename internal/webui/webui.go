package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed static
var files embed.FS

func FileSystem() http.FileSystem {
	root, err := fs.Sub(files, "static")
	if err != nil {
		panic(err)
	}
	return http.FS(root)
}
