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

func (p *Proxy) Start(ctx context.Context) error {
	target, err := url.Parse(p.Upstream)

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

			if p.Token != "" {
				req.Header.Set("Authorization", "Bearer "+p.Token)
			}

			// Disable compressed responses so captured bodies are readable.
			req.Header.Del("Accept-Encoding")

			if p.User != nil {
				if p.User.Name != "" {
					req.Header.Set("X-Forwarded-User", p.User.Name)
				}

				if p.User.Email != "" {
					req.Header.Set("X-Forwarded-Email", p.User.Email)
				}
			}
		},

		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if crw, ok := w.(*capturingResponseWriter); ok {
				crw.err = err.Error()
			}

			http.Error(w, "upstream request failed", http.StatusBadGateway)
		},
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestURL := *r.URL

		var reqBuf bytes.Buffer
		r.Body = io.NopCloser(io.TeeReader(r.Body, &reqBuf))

		crw := &capturingResponseWriter{ResponseWriter: w, status: http.StatusOK}
		rp.ServeHTTP(crw, r)

		reqBody := reqBuf.Bytes()
		respBody := crw.body.Bytes()

		entry := RequestEntry{
			Timestamp:    start,
			Method:       r.Method,
			URL:          &requestURL,
			Status:       crw.status,
			Duration:     time.Since(start),
			RequestBody:  reqBody,
			ResponseBody: respBody,
			Error:        crw.err,
		}

		if crw.err == "" {
			meta := extractMetadata(requestURL.Path, reqBody, respBody)
			entry.Model = meta.Model
			entry.InputTokens = meta.InputTokens
			entry.CachedTokens = meta.CachedTokens
			entry.OutputTokens = meta.OutputTokens
		} else {
			entry.Model = extractModel(reqBody)
		}

		p.Store.Add(entry)
	})

	server := &http.Server{
		Addr:    p.Addr,
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
	err    string
}

func (c *capturingResponseWriter) WriteHeader(status int) {
	c.status = status
	c.ResponseWriter.WriteHeader(status)
}

func (c *capturingResponseWriter) Write(b []byte) (int, error) {
	c.body.Write(b)
	return c.ResponseWriter.Write(b)
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
