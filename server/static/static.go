// Package static serves the mobile app's built web client (see
// app/README.md — `npm run build:web`, a Vite/react-native-web static
// bundle with no build-time config baked in) on its own HTTP port, entirely
// separate from api.NewRouter's port. There's nothing secret in that bundle
// (the server URL/auth token are runtime settings the user enters in-app),
// so this deliberately skips the API's auth/CORS middleware — it's just
// static assets, same trust level as any other static site. Exposing this
// port externally (reverse proxy, tunnel, etc.) is left entirely to the
// operator.
package static

import (
	"net/http"
	"os"
	"path/filepath"
)

// Serve starts a blocking HTTP server on addr, serving dir as static files
// with SPA fallback: a request for a path that doesn't exist on disk is
// served dir/index.html instead of 404ing, so react-native-web's client-side
// routing survives a refresh or deep link.
func Serve(addr, dir string) error {
	fileServer := http.FileServer(http.Dir(dir))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(p); err != nil || info.IsDir() {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		fileServer.ServeHTTP(w, r)
	})
	return http.ListenAndServe(addr, handler)
}
