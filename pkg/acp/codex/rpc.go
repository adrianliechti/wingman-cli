package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

type rpcMessage struct {
	Jsonrpc string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message) }

type rpcClient struct {
	w         io.Writer
	r         io.Reader
	writeGate chan struct{}

	nextID   atomic.Int64
	pending  sync.Map
	incoming sync.Map // request ID -> *incomingRequest

	onNotification func(method string, params json.RawMessage)
	onRequest      func(ctx context.Context, method string, params json.RawMessage) (any, *rpcError)

	done      chan struct{}
	closeOnce sync.Once
	err       error // written before done is closed
}

type incomingRequest struct {
	threadID string
	cancel   context.CancelFunc
}

func newRPCClient(w io.Writer, r io.Reader) *rpcClient {
	return &rpcClient{w: w, r: r, writeGate: make(chan struct{}, 1), done: make(chan struct{})}
}

func (c *rpcClient) start() {
	go c.readLoop()
}

func (c *rpcClient) readLoop() {
	scanner := bufio.NewScanner(c.r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			c.close(fmt.Errorf("decode app-server message: %w", err))
			return
		}

		switch {
		case msg.Method != "" && len(msg.ID) > 0:
			c.dispatchRequest(msg)
		case msg.Method != "":
			if msg.Method == "serverRequest/resolved" {
				c.resolveRequest(msg.Params)
			}
			if c.onNotification != nil {
				c.onNotification(msg.Method, msg.Params)
			}
		case len(msg.ID) > 0:
			key := string(msg.ID)
			if ch, ok := c.pending.LoadAndDelete(key); ok {
				ch.(chan rpcMessage) <- msg
			}
		}
	}
	err := scanner.Err()
	if err == nil {
		err = io.EOF
	}
	c.close(err)
}

func (c *rpcClient) close(cause error) {
	c.closeOnce.Do(func() {
		c.err = fmt.Errorf("%w: %w", errRPCClosed, cause)
		close(c.done)
		c.incoming.Range(func(_, value any) bool {
			value.(*incomingRequest).cancel()
			return true
		})
		if closer, ok := c.w.(io.Closer); ok {
			_ = closer.Close()
		}
		if closer, ok := c.r.(io.Closer); ok {
			_ = closer.Close()
		}
	})
}

func (c *rpcClient) closedError() error {
	<-c.done
	return c.err
}

func (c *rpcClient) dispatchRequest(msg rpcMessage) {
	ctx, cancel := context.WithCancel(context.Background())
	var scope struct {
		ThreadID string `json:"threadId"`
	}
	_ = json.Unmarshal(msg.Params, &scope)
	request := &incomingRequest{threadID: scope.ThreadID, cancel: cancel}
	key := string(msg.ID)
	if _, loaded := c.incoming.LoadOrStore(key, request); loaded {
		cancel()
		c.close(fmt.Errorf("duplicate app-server request id %s", key))
		return
	}
	select {
	case <-c.done:
		cancel()
	default:
	}
	go func() {
		defer cancel()
		defer c.incoming.CompareAndDelete(key, request)
		var (
			result any
			rerr   *rpcError
		)
		if c.onRequest != nil {
			result, rerr = c.onRequest(ctx, msg.Method, msg.Params)
		} else {
			rerr = &rpcError{Code: -32601, Message: "method not found"}
		}
		resp := rpcMessage{Jsonrpc: "2.0", ID: msg.ID}
		if rerr != nil {
			resp.Error = rerr
		} else {
			b, err := json.Marshal(result)
			if err != nil {
				resp.Error = &rpcError{Code: -32603, Message: err.Error()}
			} else {
				resp.Result = b
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), rpcRequestTimeout)
		defer cancel()
		_ = c.send(ctx, resp)
	}()
}

func (c *rpcClient) resolveRequest(params json.RawMessage) {
	var p struct {
		ThreadID  string          `json:"threadId"`
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	if value, ok := c.incoming.Load(string(p.RequestID)); ok {
		request := value.(*incomingRequest)
		if p.ThreadID == request.threadID {
			request.cancel()
		}
	}
}

var errRPCClosed = fmt.Errorf("codex app-server connection closed")

const rpcRequestTimeout = 30 * time.Second

func (c *rpcClient) send(ctx context.Context, msg rpcMessage) error {
	select {
	case <-c.done:
		return c.closedError()
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	msg.Jsonrpc = "2.0"
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	select {
	case c.writeGate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.closedError()
	}
	// Once a write starts, abandoning it may leave a partial JSON frame. Retire
	// the transport on cancellation instead of letting a later call use it.
	written := make(chan error, 1)
	go func() {
		defer func() { <-c.writeGate }()
		data := append(b, '\n')
		n, err := c.w.Write(data)
		if err == nil && n != len(data) {
			err = io.ErrShortWrite
		}
		written <- err
	}()
	select {
	case err := <-written:
		if err != nil {
			c.close(err)
			return c.closedError()
		}
		return nil
	case <-ctx.Done():
		c.close(fmt.Errorf("write cancelled: %w", ctx.Err()))
		return ctx.Err()
	case <-c.done:
		return c.closedError()
	}
}

func (c *rpcClient) call(ctx context.Context, method string, params any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, rpcRequestTimeout)
	defer cancel()
	id := c.nextID.Add(1)
	idRaw, _ := json.Marshal(id)
	ch := make(chan rpcMessage, 1)
	c.pending.Store(string(idRaw), ch)

	req := rpcMessage{ID: idRaw, Method: method}
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			c.pending.Delete(string(idRaw))
			return err
		}
		req.Params = b
	}
	if err := c.send(ctx, req); err != nil {
		c.pending.Delete(string(idRaw))
		select {
		case resp := <-ch:
			return decodeRPCResponse(resp, out)
		default:
			return err
		}
	}

	select {
	case <-ctx.Done():
		c.pending.Delete(string(idRaw))
		return ctx.Err()
	case <-c.done:
		c.pending.Delete(string(idRaw))
		// A peer can close immediately after replying. Preserve that reply.
		select {
		case resp := <-ch:
			return decodeRPCResponse(resp, out)
		default:
			return c.closedError()
		}
	case resp := <-ch:
		return decodeRPCResponse(resp, out)
	}
}

func decodeRPCResponse(resp rpcMessage, out any) error {
	if resp.Error != nil {
		return resp.Error
	}
	if out != nil && len(resp.Result) > 0 {
		return json.Unmarshal(resp.Result, out)
	}
	return nil
}
