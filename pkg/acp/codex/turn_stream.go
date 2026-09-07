package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
)

const (
	maxQueuedTurnEvents = 256
	maxQueuedTurnBytes  = 32 * 1024 * 1024
)

// Only enqueue on the RPC reader. The prompt consumes notifications in wire
// order after turn/start identifies their owner; client writes and approvals
// must never prevent the reader from receiving control replies or EOF.
type turnStream struct {
	ctx    context.Context
	events chan turnEvent
	bytes  atomic.Int64
	failed chan error
	ready  chan struct{}
	turnID string // published by closing ready
}

type turnEvent struct {
	rpcMessage
	processed chan struct{}
}

func newTurnStream(ctx context.Context) *turnStream {
	return &turnStream{
		ctx: ctx, events: make(chan turnEvent, maxQueuedTurnEvents),
		failed: make(chan error, 1), ready: make(chan struct{}),
	}
}

func (s *turnStream) enqueue(method string, params json.RawMessage) {
	if s.ctx.Err() != nil {
		return
	}
	size := int64(len(method) + len(params))
	if s.bytes.Add(size) <= maxQueuedTurnBytes {
		select {
		case s.events <- turnEvent{rpcMessage: rpcMessage{Method: method, Params: params}}:
			return
		default:
		}
	}
	s.bytes.Add(-size)
	select {
	case s.failed <- fmt.Errorf("Codex updates exceeded the client delivery queue"):
	default:
	}
}

func (s *turnStream) started(turnID string) {
	s.turnID = turnID
	close(s.ready)
}

func (s *turnStream) acceptsRequest(ctx context.Context, turnID string) bool {
	select {
	case <-ctx.Done():
		return false
	case <-s.ready:
		if ctx.Err() != nil || (turnID != "" && turnID != s.turnID) {
			return false
		}
	}
	// Flush earlier tool-start updates before opening a permission dialog.
	// Only the request goroutine waits; the RPC reader must remain available.
	processed := make(chan struct{})
	select {
	case s.events <- turnEvent{processed: processed}:
	case <-ctx.Done():
		return false
	}
	select {
	case <-processed:
		return ctx.Err() == nil
	case <-ctx.Done():
		return false
	}
}

func (s *turnStream) owns(event rpcMessage) (bool, error) {
	var p struct {
		TurnID string `json:"turnId"`
		Turn   *struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(event.Params, &p); err != nil {
		return false, fmt.Errorf("decode %s scope: %w", event.Method, err)
	}
	if event.Method == "turn/completed" && (p.Turn == nil || p.Turn.ID == "") {
		return false, fmt.Errorf("turn/completed is missing its turn id")
	}
	if p.Turn != nil {
		return p.Turn.ID == s.turnID, nil
	}
	return p.TurnID == "" || p.TurnID == s.turnID, nil
}

func (s *turnStream) next(rpc *rpcClient) (turnEvent, error) {
	var event turnEvent
	select {
	case err := <-s.failed:
		return event, err
	default:
	}
	select {
	case <-s.ctx.Done():
		return event, s.ctx.Err()
	default:
	}
	// Drain already-read events before EOF, including a final turn/completed.
	select {
	case event = <-s.events:
	default:
		select {
		case event = <-s.events:
		case <-s.ctx.Done():
			return event, s.ctx.Err()
		case err := <-s.failed:
			return event, err
		case <-rpc.done:
			select {
			case event = <-s.events:
			default:
				return event, rpc.closedError()
			}
		}
	}
	s.bytes.Add(-int64(len(event.Method) + len(event.Params)))
	return event, nil
}
