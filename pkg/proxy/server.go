package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

type contextKey struct{}

func startServer(ctx context.Context, addr, upstream, token string, user *UserInfo, store *Store) error {
	target, err := url.Parse(upstream)

	if err != nil {
		return fmt.Errorf("invalid upstream URL: %w", err)
	}

	rp := &httputil.ReverseProxy{
		FlushInterval: -1,

		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.URL.Path = target.Path + req.URL.Path
			req.Host = ""

			if token != "" {
				req.Header.Set("Authorization", "Bearer "+token)
			}

			if user != nil {
				if user.Name != "" {
					req.Header.Set("X-Forwarded-User", user.Name)
				}

				if user.Email != "" {
					req.Header.Set("X-Forwarded-Email", user.Email)
				}
			}
		},

		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if p, ok := r.Context().Value(contextKey{}).(*string); ok {
				*p = err.Error()
			}

			http.Error(w, "upstream request failed", http.StatusBadGateway)
		},
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		reqBody, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusBadRequest)
			return
		}

		r.Body = io.NopCloser(bytes.NewReader(reqBody))

		var upstreamErr string
		r = r.WithContext(context.WithValue(r.Context(), contextKey{}, &upstreamErr))

		crw := &capturingResponseWriter{ResponseWriter: w, status: http.StatusOK}
		rp.ServeHTTP(crw, r)

		respBody := crw.Bytes()
		streaming := extractStreaming(reqBody)

		entry := RequestEntry{
			Timestamp:    start,
			Method:       r.Method,
			Path:         r.URL.Path,
			Status:       crw.status,
			Duration:     time.Since(start),
			Streaming:    streaming,
			RequestBody:  reqBody,
			ResponseBody: respBody,
			Error:        upstreamErr,
		}

		if upstreamErr == "" {
			var meta Metadata
			if streaming {
				meta = extractMetadataSSE(r.URL.Path, reqBody, respBody)
			} else {
				meta = extractMetadata(r.URL.Path, reqBody, respBody)
			}

			entry.Model = meta.Model
			entry.InputTokens = meta.InputTokens
			entry.OutputTokens = meta.OutputTokens
		} else {
			entry.Model = extractModel(reqBody)
		}

		store.Add(entry)
	})

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		server.Shutdown(shutdownCtx)
	}()

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("proxy server: %w", err)
	}

	return nil
}

type capturingResponseWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

func (c *capturingResponseWriter) WriteHeader(status int) {
	c.status = status
	c.ResponseWriter.WriteHeader(status)
}

func (c *capturingResponseWriter) Write(b []byte) (int, error) {
	_, _ = c.body.Write(b)
	return c.ResponseWriter.Write(b)
}

func (c *capturingResponseWriter) Bytes() []byte {
	return c.body.Bytes()
}

func (c *capturingResponseWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func extractModel(body []byte) string {
	if len(body) == 0 {
		return ""
	}

	var obj struct {
		Model string `json:"model"`
	}

	if json.Unmarshal(body, &obj) == nil {
		return obj.Model
	}

	return ""
}

func extractStreaming(body []byte) bool {
	if len(body) == 0 {
		return false
	}

	var obj struct {
		Stream *bool `json:"stream"`
	}

	if json.Unmarshal(body, &obj) == nil && obj.Stream != nil {
		return *obj.Stream
	}

	return false
}
