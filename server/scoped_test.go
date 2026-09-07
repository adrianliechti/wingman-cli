package server

import "net/http"

func serveTestHTTP(app *Server, w http.ResponseWriter, r *http.Request) {
	r.Header.Set(instanceHeader, app.scope.InstanceID)
	app.ServeHTTP(w, r)
}
func scopedTestHandler(app *Server) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { serveTestHTTP(app, w, r) })
}
