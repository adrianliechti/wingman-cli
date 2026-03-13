package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func startServer(ctx context.Context, addr, upstream, token string, store *Store) error {
	upstream = strings.TrimRight(upstream, "/")

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handleProxy(w, r, upstream, token, store)
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

func handleProxy(w http.ResponseWriter, r *http.Request, upstream, token string, store *Store) {
	start := time.Now()

	reqBody, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read request body", http.StatusBadGateway)
		return
	}
	r.Body.Close()

	model := extractModel(reqBody)
	streaming := extractStreaming(reqBody)

	targetURL := upstream + r.URL.Path
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, bytes.NewReader(reqBody))
	if err != nil {
		http.Error(w, "failed to create upstream request", http.StatusBadGateway)
		return
	}

	for key, values := range r.Header {
		for _, v := range values {
			proxyReq.Header.Add(key, v)
		}
	}

	proxyReq.Header.Del("Host")

	if token != "" && proxyReq.Header.Get("Authorization") == "" {
		proxyReq.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(proxyReq)
	if err != nil {
		entry := RequestEntry{
			Timestamp:   start,
			Method:      r.Method,
			Path:        r.URL.Path,
			Duration:    time.Since(start),
			Model:       model,
			Streaming:   streaming,
			RequestBody: reqBody,
			Error:       err.Error(),
		}
		store.Add(entry)

		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	entry := RequestEntry{
		Timestamp:   start,
		Method:      r.Method,
		Path:        r.URL.Path,
		Status:      resp.StatusCode,
		Model:       model,
		Streaming:   streaming,
		RequestBody: reqBody,
	}

	if streaming && resp.StatusCode == http.StatusOK && strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		respBody := handleSSE(w, resp, r)
		entry.ResponseBody = respBody
		entry.Duration = time.Since(start)

		meta := extractMetadataSSE(r.URL.Path, reqBody, respBody)
		entry.Model = meta.Model
		entry.InputTokens = meta.InputTokens
		entry.OutputTokens = meta.OutputTokens
	} else {
		respBody, _ := io.ReadAll(resp.Body)
		entry.ResponseBody = respBody
		entry.Duration = time.Since(start)

		meta := extractMetadata(r.URL.Path, reqBody, respBody)
		entry.Model = meta.Model
		entry.InputTokens = meta.InputTokens
		entry.OutputTokens = meta.OutputTokens

		for key, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
	}

	store.Add(entry)
}

func handleSSE(w http.ResponseWriter, resp *http.Response, r *http.Request) []byte {
	flusher, ok := w.(http.Flusher)
	if !ok {
		body, _ := io.ReadAll(resp.Body)
		w.Write(body)
		return body
	}

	for key, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	var buf bytes.Buffer
	scanner := bufio.NewScanner(resp.Body)

	// increase buffer for large SSE chunks
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		buf.WriteString(line)
		buf.WriteString("\n")

		fmt.Fprintf(w, "%s\n", line)
		flusher.Flush()
	}

	return buf.Bytes()
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
		Stream any `json:"stream"`
	}

	if json.Unmarshal(body, &obj) == nil {
		return obj.Stream != nil && obj.Stream != false
	}

	return false
}
