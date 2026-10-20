package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
)

// The MCP subset this server speaks: newline-delimited JSON-RPC 2.0 over
// stdio with initialize, ping, tools, prompts and cancellation. It is
// hand-written so the server can send Claude Code's channel notifications,
// which typed SDKs do not expose.

const defaultProtocolVersion = "2025-06-18"

var knownProtocolVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return e.Message }

type rpcIncoming struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResult struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result"`
}

type rpcErrorReply struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Error   *rpcError       `json:"error"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type server struct {
	b       *hub
	channel bool
	tools   []tool

	wmu sync.Mutex
	w   io.Writer

	imu      sync.Mutex
	inflight map[string]context.CancelFunc

	startOnce sync.Once
	wg        sync.WaitGroup
}

func newServer(b *hub, channel bool) *server {
	s := &server{b: b, channel: channel, inflight: map[string]context.CancelFunc{}}
	s.tools = s.toolList()
	return s
}

// serve reads requests from r until EOF and writes replies to w. Requests
// run concurrently, since wait_for_message blocks until the user replies.
func (s *server) serve(ctx context.Context, r io.Reader, w io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		s.wg.Wait()
	}()
	s.w = w
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var msg rpcIncoming
		if err := json.Unmarshal(line, &msg); err != nil {
			s.replyError(json.RawMessage("null"), &rpcError{Code: codeParseError, Message: "parse error"})
			continue
		}
		switch {
		case msg.Method == "":
			// A response to a request this server never sends.
		case len(msg.ID) == 0:
			s.handleNotification(ctx, msg)
		default:
			reqCtx, reqCancel := context.WithCancel(ctx)
			key := string(msg.ID)
			s.imu.Lock()
			_, dup := s.inflight[key]
			if !dup {
				s.inflight[key] = reqCancel
			}
			s.imu.Unlock()
			if dup {
				reqCancel()
				s.replyError(msg.ID, &rpcError{Code: codeInvalidRequest, Message: "request id already in use"})
				continue
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer func() {
					s.imu.Lock()
					delete(s.inflight, key)
					s.imu.Unlock()
					reqCancel()
				}()
				result, err := s.handleRequest(reqCtx, msg)
				if reqCtx.Err() != nil && ctx.Err() == nil {
					return // cancelled by the client, which expects no reply
				}
				if err != nil {
					var re *rpcError
					if !errors.As(err, &re) {
						re = &rpcError{Code: codeInvalidParams, Message: err.Error()}
					}
					s.replyError(msg.ID, re)
					return
				}
				s.write(rpcResult{JSONRPC: "2.0", ID: msg.ID, Result: result})
			}()
		}
	}
	return sc.Err()
}

func (s *server) handleNotification(ctx context.Context, msg rpcIncoming) {
	switch msg.Method {
	case "notifications/initialized":
		if s.channel {
			s.startOnce.Do(func() {
				s.wg.Add(1)
				go func() {
					defer s.wg.Done()
					s.relayMessages(ctx)
				}()
			})
		}
	case "notifications/cancelled":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(msg.Params, &p) == nil {
			s.imu.Lock()
			if cancel, ok := s.inflight[string(p.RequestID)]; ok {
				cancel()
			}
			s.imu.Unlock()
		}
	}
}

func (s *server) handleRequest(ctx context.Context, msg rpcIncoming) (any, error) {
	switch msg.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		proto := defaultProtocolVersion
		if slices.Contains(knownProtocolVersions, p.ProtocolVersion) {
			proto = p.ProtocolVersion
		}
		caps := map[string]any{"tools": map[string]any{}, "prompts": map[string]any{}}
		if s.channel {
			caps["experimental"] = map[string]any{"claude/channel": map[string]any{}}
		}
		return map[string]any{
			"protocolVersion": proto,
			"capabilities":    caps,
			"serverInfo":      map[string]any{"name": "ttt-tour", "version": version},
			"instructions":    s.instructions(),
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		list := make([]map[string]any, len(s.tools))
		for i, t := range s.tools {
			list[i] = map[string]any{"name": t.name, "description": t.description, "inputSchema": t.schema}
		}
		return map[string]any{"tools": list}, nil
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
		}
		i := slices.IndexFunc(s.tools, func(t tool) bool { return t.name == p.Name })
		if i < 0 {
			return nil, &rpcError{Code: codeInvalidParams, Message: "unknown tool " + p.Name}
		}
		args := p.Arguments
		if len(args) == 0 || string(args) == "null" {
			args = json.RawMessage("{}")
		}
		text, err := s.tools[i].run(ctx, args)
		if err != nil {
			return toolResult(err.Error(), true), nil
		}
		return toolResult(text, false), nil
	case "prompts/list":
		return map[string]any{"prompts": promptList()}, nil
	case "prompts/get":
		var p struct {
			Name      string            `json:"name"`
			Arguments map[string]string `json:"arguments"`
		}
		if err := json.Unmarshal(msg.Params, &p); err != nil {
			return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
		}
		return getPrompt(p.Name, p.Arguments)
	}
	return nil, &rpcError{Code: codeMethodNotFound, Message: "method not found: " + msg.Method}
}

func toolResult(text string, isError bool) map[string]any {
	res := map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
	if isError {
		res["isError"] = true
	}
	return res
}

func (s *server) notify(method string, params any) {
	s.write(rpcNotification{JSONRPC: "2.0", Method: method, Params: params})
}

func (s *server) replyError(id json.RawMessage, e *rpcError) {
	s.write(rpcErrorReply{JSONRPC: "2.0", ID: id, Error: e})
}

func (s *server) write(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		data, _ = json.Marshal(rpcNotification{JSONRPC: "2.0", Method: "notifications/message",
			Params: map[string]any{"level": "error", "data": fmt.Sprintf("encoding reply: %v", err)}})
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_, _ = s.w.Write(append(data, '\n'))
}
