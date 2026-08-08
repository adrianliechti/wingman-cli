package jsonrpc2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

type Binder interface {
	Bind(context.Context, *Connection) ConnectionOptions
}

type BinderFunc func(context.Context, *Connection) ConnectionOptions

func (f BinderFunc) Bind(ctx context.Context, c *Connection) ConnectionOptions {
	return f(ctx, c)
}

var _ Binder = BinderFunc(nil)

type ConnectionOptions struct {
	Framer Framer

	Preempter Preempter

	Handler Handler

	OnInternalError func(error)
}

type Connection struct {
	seq atomic.Int64

	stateMu sync.Mutex
	state   inFlightState
	done    chan struct{}

	writer  Writer
	handler Handler

	onInternalError func(error)
	onDone          func()
}

type inFlightState struct {
	connClosing bool
	reading     bool
	readErr     error
	writeErr    error

	closer   io.Closer
	closeErr error

	outgoingCalls         map[ID]*AsyncCall
	outgoingNotifications int

	incoming int

	incomingByID map[ID]*incomingRequest

	handlerQueue   []*incomingRequest
	handlerRunning bool
}

func (c *Connection) updateInFlight(f func(*inFlightState)) {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	s := &c.state

	f(s)

	select {
	case <-c.done:

		if !s.idle() {
			panic("jsonrpc2: updateInFlight transitioned to non-idle when already done")
		}
		return
	default:
	}

	if s.idle() && s.shuttingDown(ErrUnknown) != nil {
		if s.closer != nil {
			s.closeErr = s.closer.Close()
			s.closer = nil
		}
		if s.reading {

		} else {

			if c.onDone != nil {
				c.onDone()
			}
			close(c.done)
		}
	}
}

func (s *inFlightState) idle() bool {
	return len(s.outgoingCalls) == 0 && s.outgoingNotifications == 0 && s.incoming == 0 && !s.handlerRunning
}

func (s *inFlightState) shuttingDown(errClosing error) error {
	if s.connClosing {

		return errClosing
	}
	if s.readErr != nil {

		return fmt.Errorf("%w: %v", errClosing, s.readErr)
	}
	if s.writeErr != nil {

		return fmt.Errorf("%w: %v", errClosing, s.writeErr)
	}
	return nil
}

type incomingRequest struct {
	*Request
	ctx    context.Context
	cancel context.CancelFunc
}

func (o ConnectionOptions) Bind(context.Context, *Connection) ConnectionOptions {
	return o
}

type ConnectionConfig struct {
	Reader          Reader
	Writer          Writer
	Closer          io.Closer
	Preempter       Preempter
	Bind            func(*Connection) Handler
	OnDone          func()
	OnInternalError func(error)
}

func NewConnection(ctx context.Context, cfg ConnectionConfig) *Connection {
	ctx = notDone{ctx}

	c := &Connection{
		state:           inFlightState{closer: cfg.Closer},
		done:            make(chan struct{}),
		writer:          cfg.Writer,
		onDone:          cfg.OnDone,
		onInternalError: cfg.OnInternalError,
	}
	c.handler = cfg.Bind(c)
	c.start(ctx, cfg.Reader, cfg.Preempter)
	return c
}

func bindConnection(bindCtx context.Context, rwc io.ReadWriteCloser, binder Binder, onDone func()) *Connection {

	ctx := notDone{bindCtx}

	c := &Connection{
		state:  inFlightState{closer: rwc},
		done:   make(chan struct{}),
		onDone: onDone,
	}

	options := binder.Bind(bindCtx, c)
	framer := options.Framer
	if framer == nil {
		framer = HeaderFramer()
	}
	c.handler = options.Handler
	if c.handler == nil {
		c.handler = defaultHandler{}
	}
	c.onInternalError = options.OnInternalError

	c.writer = framer.Writer(rwc)
	reader := framer.Reader(rwc)
	c.start(ctx, reader, options.Preempter)
	return c
}

func (c *Connection) start(ctx context.Context, reader Reader, preempter Preempter) {
	c.updateInFlight(func(s *inFlightState) {
		select {
		case <-c.done:

			return
		default:
		}

		s.reading = true
		go c.readIncoming(ctx, reader, preempter)
	})
}

func (c *Connection) Notify(ctx context.Context, method string, params any) (err error) {
	attempted := false

	defer func() {
		if attempted {
			c.updateInFlight(func(s *inFlightState) {
				s.outgoingNotifications--
			})
		}
	}()

	c.updateInFlight(func(s *inFlightState) {

		if len(s.outgoingCalls) == 0 && len(s.incomingByID) == 0 {
			err = s.shuttingDown(ErrClientClosing)
			if err != nil {
				return
			}
		}
		s.outgoingNotifications++
		attempted = true
	})
	if err != nil {
		return err
	}

	notify, err := NewNotification(method, params)
	if err != nil {
		return fmt.Errorf("marshaling notify parameters: %v", err)
	}

	return c.write(ctx, notify)
}

func (c *Connection) Call(ctx context.Context, method string, params any) *AsyncCall {

	id := Int64ID(c.seq.Add(1))

	ac := &AsyncCall{
		id:    id,
		ready: make(chan struct{}),
	}

	call, err := NewCall(ac.id, method, params)
	if err != nil {
		ac.retire(&Response{ID: id, Error: fmt.Errorf("marshaling call parameters: %w", err)})
		return ac
	}

	c.updateInFlight(func(s *inFlightState) {
		err = s.shuttingDown(ErrClientClosing)
		if err != nil {
			return
		}
		if s.outgoingCalls == nil {
			s.outgoingCalls = make(map[ID]*AsyncCall)
		}
		s.outgoingCalls[ac.id] = ac
	})
	if err != nil {
		ac.retire(&Response{ID: id, Error: err})
		return ac
	}

	if err := c.write(ctx, call); err != nil {

		c.Retire(ac, err)
	}
	return ac
}

func (c *Connection) Retire(ac *AsyncCall, err error) {
	c.updateInFlight(func(s *inFlightState) {
		if s.outgoingCalls[ac.id] == ac {
			delete(s.outgoingCalls, ac.id)
			ac.retire(&Response{ID: ac.id, Error: err})
		} else {

		}
	})
}

func Async(ctx context.Context) {
	if r, ok := ctx.Value(asyncKey).(*releaser); ok {
		r.release(false)
	}
}

type asyncKeyType struct{}

var asyncKey = asyncKeyType{}

type releaser struct {
	mu       sync.Mutex
	ch       chan struct{}
	released bool
}

func (r *releaser) release(soft bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.released {
		if !soft {
			panic("jsonrpc2.Async called multiple times")
		}
	} else {
		close(r.ch)
		r.released = true
	}
}

type AsyncCall struct {
	id       ID
	ready    chan struct{}
	response *Response
}

func (ac *AsyncCall) ID() ID { return ac.id }

func (ac *AsyncCall) IsReady() bool {
	select {
	case <-ac.ready:
		return true
	default:
		return false
	}
}

func (ac *AsyncCall) retire(response *Response) {
	select {
	case <-ac.ready:
		panic(fmt.Sprintf("jsonrpc2: retire called twice for ID %v", ac.id))
	default:
	}

	ac.response = response
	close(ac.ready)
}

func (ac *AsyncCall) Await(ctx context.Context, result any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ac.ready:
	}
	if ac.response.Error != nil {
		return ac.response.Error
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(ac.response.Result, result)
}

func (c *Connection) Cancel(id ID) {
	var req *incomingRequest
	c.updateInFlight(func(s *inFlightState) {
		req = s.incomingByID[id]
	})
	if req != nil {
		req.cancel()
	}
}

func (c *Connection) Wait() error {
	return c.wait(true)
}

func (c *Connection) wait(fromWait bool) error {
	var err error
	<-c.done
	c.updateInFlight(func(s *inFlightState) {
		if fromWait {
			if !errors.Is(s.readErr, io.EOF) {
				err = s.readErr
			}
			if err == nil && !errors.Is(s.writeErr, io.EOF) {
				err = s.writeErr
			}
		}
		if err == nil {
			err = s.closeErr
		}
	})
	return err
}

func (c *Connection) Close() error {

	c.updateInFlight(func(s *inFlightState) { s.connClosing = true })
	return c.wait(false)
}

func (c *Connection) readIncoming(ctx context.Context, reader Reader, preempter Preempter) {
	var err error
	for {
		var msg Message
		msg, err = reader.Read(ctx)
		if err != nil {
			break
		}

		switch msg := msg.(type) {
		case *Request:
			c.acceptRequest(ctx, msg, preempter)

		case *Response:
			c.updateInFlight(func(s *inFlightState) {
				if ac, ok := s.outgoingCalls[msg.ID]; ok {
					delete(s.outgoingCalls, msg.ID)
					ac.retire(msg)
				} else {

				}
			})

		default:
			c.internalErrorf("Read returned an unexpected message of type %T", msg)
		}
	}

	c.updateInFlight(func(s *inFlightState) {
		s.reading = false
		s.readErr = err

		for id, ac := range s.outgoingCalls {
			ac.retire(&Response{ID: id, Error: err})
		}
		s.outgoingCalls = nil
	})
}

func (c *Connection) acceptRequest(ctx context.Context, msg *Request, preempter Preempter) {

	reqCtx, cancel := context.WithCancel(ctx)
	req := &incomingRequest{
		Request: msg,
		ctx:     reqCtx,
		cancel:  cancel,
	}

	var err error
	c.updateInFlight(func(s *inFlightState) {
		s.incoming++

		if req.IsCall() {
			if s.incomingByID[req.ID] != nil {
				err = fmt.Errorf("%w: request ID %v already in use", ErrInvalidRequest, req.ID)
				req.ID = ID{}
				return
			}

			if s.incomingByID == nil {
				s.incomingByID = make(map[ID]*incomingRequest)
			}
			s.incomingByID[req.ID] = req

			err = s.shuttingDown(ErrServerClosing)
		}
	})
	if err != nil {
		c.processResult("acceptRequest", req, nil, err)
		return
	}

	if preempter != nil {
		result, err := preempter.Preempt(req.ctx, req.Request)

		if !errors.Is(err, ErrNotHandled) {
			c.processResult("Preempt", req, result, err)
			return
		}
	}

	c.updateInFlight(func(s *inFlightState) {

		err = s.shuttingDown(ErrServerClosing)
		if err != nil {
			return
		}

		s.handlerQueue = append(s.handlerQueue, req)
		if !s.handlerRunning {

			s.handlerRunning = true
			go c.handleAsync()
		}
	})
	if err != nil {
		c.processResult("acceptRequest", req, nil, err)
	}
}

func (c *Connection) handleAsync() {
	for {
		var req *incomingRequest
		c.updateInFlight(func(s *inFlightState) {
			if len(s.handlerQueue) > 0 {
				req, s.handlerQueue = s.handlerQueue[0], s.handlerQueue[1:]
			} else {
				s.handlerRunning = false
			}
		})
		if req == nil {
			return
		}

		if err := req.ctx.Err(); err != nil {
			c.updateInFlight(func(s *inFlightState) {
				if s.writeErr != nil {

					err = fmt.Errorf("%w: %v", ErrServerClosing, s.writeErr)
				}
			})
			c.processResult("handleAsync", req, nil, err)
			continue
		}

		releaser := &releaser{ch: make(chan struct{})}
		ctx := context.WithValue(req.ctx, asyncKey, releaser)
		go func() {
			defer releaser.release(true)
			result, err := c.handler.Handle(ctx, req.Request)
			c.processResult(c.handler, req, result, err)
		}()
		<-releaser.ch
	}
}

func (c *Connection) processResult(from any, req *incomingRequest, result any, err error) error {
	switch err {
	case ErrNotHandled, ErrMethodNotFound:

		err = fmt.Errorf("%w: %q", ErrMethodNotFound, req.Method)
	}

	if result != nil && err != nil {
		c.internalErrorf("%#v returned a non-nil result with a non-nil error for %s:\n%v\n%#v", from, req.Method, err, result)
		result = nil
	}

	if req.IsCall() {
		if result == nil && err == nil {
			err = c.internalErrorf("%#v returned a nil result and nil error for a %q Request that requires a Response", from, req.Method)
		}

		response, respErr := NewResponse(req.ID, result, err)

		c.updateInFlight(func(s *inFlightState) {
			delete(s.incomingByID, req.ID)
		})
		if respErr == nil {
			writeErr := c.write(notDone{req.ctx}, response)
			if err == nil {
				err = writeErr
			}
		} else {
			err = c.internalErrorf("%#v returned a malformed result for %q: %w", from, req.Method, respErr)
		}
	} else {
		if result != nil {
			err = c.internalErrorf("%#v returned a non-nil result for a %q Request without an ID", from, req.Method)
		} else if err != nil {
			err = fmt.Errorf("%w: %q notification failed: %v", ErrInternal, req.Method, err)
		}
	}
	if err != nil {

	}

	req.cancel()
	c.updateInFlight(func(s *inFlightState) {
		if s.incoming == 0 {
			panic("jsonrpc2: processResult called when incoming count is already zero")
		}
		s.incoming--
	})
	return nil
}

func (c *Connection) write(ctx context.Context, msg Message) error {
	var err error

	c.updateInFlight(func(s *inFlightState) {
		err = s.shuttingDown(ErrServerClosing)
	})
	if err == nil {
		err = c.writer.Write(ctx, msg)
	}

	if err != nil && ctx.Err() == nil && !errors.Is(err, ErrRejected) {

		c.updateInFlight(func(s *inFlightState) {
			if s.writeErr == nil {
				s.writeErr = err
				for _, r := range s.incomingByID {
					r.cancel()
				}
			}
		})
	}

	return err
}

func (c *Connection) internalErrorf(format string, args ...any) error {
	err := fmt.Errorf(format, args...)
	if c.onInternalError == nil {
		panic("jsonrpc2: " + err.Error())
	}
	c.onInternalError(err)

	return fmt.Errorf("%w: %v", ErrInternal, err)
}

type notDone struct{ ctx context.Context }

func (ic notDone) Value(key any) any {
	return ic.ctx.Value(key)
}

func (notDone) Done() <-chan struct{}       { return nil }
func (notDone) Err() error                  { return nil }
func (notDone) Deadline() (time.Time, bool) { return time.Time{}, false }
