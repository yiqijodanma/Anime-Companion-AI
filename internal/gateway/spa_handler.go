package gateway

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"companion-ai/internal/webui"
)

func (h *Handlers) registerWebUI(router *gin.Engine) {
	spa := gin.WrapH(webui.SPAHandler())
	router.GET("/", spa)
	router.GET("/assets/*filepath", spa)
	// Preserve the previous local URL while production and bookmarks move to root.
	router.StaticFS("/app", webui.FileSystem())
	router.NoRoute(func(c *gin.Context) {
		if (c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead) && !isReservedBackendPath(c.Request.URL.Path) {
			spa(c)
			return
		}
		c.Status(http.StatusNotFound)
	})
}

func isReservedBackendPath(requestPath string) bool {
	for _, prefix := range []string{"/api", "/healthz", "/livez", "/readyz", "/wechat"} {
		if requestPath == prefix || strings.HasPrefix(requestPath, prefix+"/") {
			return true
		}
	}
	return false
}
