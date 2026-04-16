package api

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// SaneAPIProxy 将 /sane-api 下的请求反向代理到 scanservjs。
func SaneAPIProxy() gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg, err := requireConfig()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		targetURL := strings.TrimSpace(cfg.SaneAPI.TargetURL)
		target, err := url.Parse(targetURL)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "invalid sane_api.target_url"})
			return
		}

		forwardPath := strings.TrimPrefix(c.Request.URL.Path, "/sane-api")
		if forwardPath == "" {
			forwardPath = "/"
		}

		proxy := httputil.NewSingleHostReverseProxy(target)
		origDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			origDirector(req)
			req.URL.Path = forwardPath
			req.URL.RawPath = ""
			req.Host = target.Host
			req.Header.Set("X-Forwarded-Host", c.Request.Host)
			req.Header.Set("X-Forwarded-Proto", forwardedProto(c.Request))
			req.Header.Set("X-Forwarded-Prefix", "/sane-api")
		}
		proxy.ModifyResponse = func(resp *http.Response) error {
			location := strings.TrimSpace(resp.Header.Get("Location"))
			if location == "" {
				return nil
			}

			rewritten, ok := rewriteProxyLocation(location, target)
			if ok {
				resp.Header.Set("Location", rewritten)
			}

			return nil
		}
		proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, proxyErr error) {
			log.Printf("[sane-api][proxy] upstream error method=%s path=%s err=%v", req.Method, req.URL.Path, proxyErr)
			rw.Header().Set("Content-Type", "application/json")
			rw.WriteHeader(http.StatusBadGateway)
			_, _ = rw.Write([]byte(`{"error":"bad gateway"}`))
		}

		proxy.ServeHTTP(c.Writer, c.Request)
	}
}

func rewriteProxyLocation(location string, target *url.URL) (string, bool) {
	const prefix = "/sane-api"

	if strings.HasPrefix(location, "/") {
		if location == prefix || strings.HasPrefix(location, prefix+"/") {
			return location, false
		}
		return prefix + location, true
	}

	u, err := url.Parse(location)
	if err != nil || !u.IsAbs() {
		return location, false
	}

	if !sameUpstream(u, target) {
		return location, false
	}

	path := u.Path
	if path == "" {
		path = "/"
	}
	if path != prefix && !strings.HasPrefix(path, prefix+"/") {
		u.Path = prefix + path
	}

	u.Scheme = ""
	u.Host = ""
	return u.String(), true
}

func sameUpstream(u *url.URL, target *url.URL) bool {
	if !strings.EqualFold(u.Hostname(), target.Hostname()) {
		return false
	}

	uPort := u.Port()
	tPort := target.Port()
	if uPort == "" {
		uPort = defaultPort(u.Scheme)
	}
	if tPort == "" {
		tPort = defaultPort(target.Scheme)
	}

	return uPort == tPort
}

func defaultPort(scheme string) string {
	switch strings.ToLower(strings.TrimSpace(scheme)) {
	case "https":
		return "443"
	default:
		return "80"
	}
}

func forwardedProto(req *http.Request) string {
	if req.TLS != nil {
		return "https"
	}
	if proto := strings.TrimSpace(req.Header.Get("X-Forwarded-Proto")); proto != "" {
		return proto
	}
	return "http"
}
