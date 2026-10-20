package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const (
	// connectedGrace is how long after its last poll the plugin still counts
	// as connected.
	connectedGrace = 5 * time.Second
	maxPollWait    = 25 * time.Second
	callTimeout    = 15 * time.Second
	maxBody        = 8 << 20
)

// errNoMessage reports that a wait for the user's messages timed out.
var errNoMessage = errors.New("no message yet")

// command is an operation for the tour plugin to carry out in ttt.
type command struct {
	ID   int    `json:"id"`
	Op   string `json:"op"`
	Args any    `json:"args,omitempty"`
}

type result struct {
	Data  json.RawMessage
	Error string
}

// userMessage is what the user typed in the Tour pane. At is the step on
// screen when it arrived.
type userMessage struct {
	Text string `json:"text"`
	At   string `json:"at,omitempty"`
}

// hub is the loopback HTTP server the ttt tour plugin talks to. The plugin
// long-polls GET /poll for commands, acknowledging the ones it has carried
// out with after=ID, posts each command's outcome to /result and the user's
// messages to /message. Commands stay queued until acknowledged, so a plugin
// that reloads mid-command runs it again rather than losing it.
type hub struct {
	mu       sync.Mutex
	queue    []command
	nextID   int
	waiting  map[int]chan result
	cmdWake  chan struct{}
	polling  int
	lastPoll time.Time
	inbox    []userMessage
	seen     map[string]bool
	msgWake  chan struct{}
	at       string

	// addr is where the plugin connects; empty when something else serves
	// the handler (tests).
	addr      string
	lmu       sync.Mutex
	listening bool
}

func newHub() *hub {
	return &hub{
		// The plugin outlives ttt-mcp and skips IDs it has already carried
		// out, so IDs keep increasing across restarts.
		nextID:  int(time.Now().UnixMilli()),
		waiting: map[int]chan result{},
		cmdWake: make(chan struct{}),
		msgWake: make(chan struct{}),
		seen:    map[string]bool{},
	}
}

func (h *hub) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /poll", h.handlePoll)
	mux.HandleFunc("POST /result", h.handleResult)
	mux.HandleFunc("POST /message", h.handleMessage)
	return mux
}

// listen serves the plugin on h.addr unless it already does. Tool calls run
// it again, so a ttt-mcp that started while another held the port takes
// over once that one quits.
func (h *hub) listen() error {
	h.lmu.Lock()
	defer h.lmu.Unlock()
	if h.addr == "" || h.listening {
		return nil
	}
	ln, err := net.Listen("tcp", h.addr)
	if errors.Is(err, syscall.EADDRINUSE) {
		return fmt.Errorf("%s is in use, probably by ttt-mcp for another agent session; quit that session and try again", h.addr)
	}
	if err != nil {
		return fmt.Errorf("cannot listen for the tour plugin: %w", err)
	}
	h.listening = true
	go func() {
		if err := http.Serve(ln, h.handler()); err != nil && !errors.Is(err, net.ErrClosed) {
			log.Print(err)
		}
	}()
	return nil
}

func (h *hub) connected() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.polling > 0 || time.Since(h.lastPoll) < connectedGrace
}

func (h *hub) handlePoll(w http.ResponseWriter, r *http.Request) {
	after, _ := strconv.Atoi(r.URL.Query().Get("after"))
	wait := maxPollWait
	if s, err := strconv.Atoi(r.URL.Query().Get("wait")); err == nil {
		wait = min(time.Duration(s)*time.Second, maxPollWait)
	}
	h.mu.Lock()
	h.polling++
	h.ack(after)
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		h.polling--
		h.lastPoll = time.Now()
		h.mu.Unlock()
	}()

	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		h.mu.Lock()
		cmds := append([]command(nil), h.queue...)
		wake := h.cmdWake
		h.mu.Unlock()
		if len(cmds) > 0 {
			writeJSON(w, cmds)
			return
		}
		select {
		case <-wake:
		case <-timer.C:
			writeJSON(w, []command{})
			return
		case <-r.Context().Done():
			return
		}
	}
}

// ack drops the commands the plugin has carried out. Callers hold h.mu.
func (h *hub) ack(after int) {
	i := 0
	for i < len(h.queue) && h.queue[i].ID <= after {
		i++
	}
	h.queue = h.queue[i:]
}

func (h *hub) handleResult(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID    int             `json:"id"`
		Data  json.RawMessage `json:"data"`
		Error string          `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&in); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	ch := h.waiting[in.ID]
	delete(h.waiting, in.ID)
	h.mu.Unlock()
	if ch != nil {
		ch <- result{Data: in.Data, Error: in.Error}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *hub) handleMessage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(&in); err != nil || in.ID == "" {
		http.Error(w, "want {id, text}", http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	if !h.seen[in.ID] {
		h.seen[in.ID] = true
		h.inbox = append(h.inbox, userMessage{Text: in.Text, At: h.at})
		close(h.msgWake)
		h.msgWake = make(chan struct{})
	}
	h.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

var errNotConnected = errors.New("the ttt tour plugin is not connected: start ttt with the tour plugin enabled (see the ttt-tour README)")

// call queues op for the plugin and waits for its outcome.
func (h *hub) call(ctx context.Context, op string, args any) (json.RawMessage, error) {
	if err := h.listen(); err != nil {
		return nil, err
	}
	if !h.connected() {
		return nil, errNotConnected
	}
	ch := make(chan result, 1)
	h.mu.Lock()
	h.nextID++
	id := h.nextID
	h.queue = append(h.queue, command{ID: id, Op: op, Args: args})
	h.waiting[id] = ch
	close(h.cmdWake)
	h.cmdWake = make(chan struct{})
	h.mu.Unlock()

	timer := time.NewTimer(callTimeout)
	defer timer.Stop()
	select {
	case res := <-ch:
		if res.Error != "" {
			return nil, fmt.Errorf("ttt: %s", res.Error)
		}
		return res.Data, nil
	case <-timer.C:
		h.drop(id)
		return nil, fmt.Errorf("ttt did not answer within %s", callTimeout)
	case <-ctx.Done():
		h.drop(id)
		return nil, ctx.Err()
	}
}

// drop forgets a command nobody waits for, so a plugin that connects later
// does not replay it.
func (h *hub) drop(id int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.waiting, id)
	for i, c := range h.queue {
		if c.ID == id {
			h.queue = append(h.queue[:i:i], h.queue[i+1:]...)
			return
		}
	}
}

// setLocation records the step on screen, which messages from the user
// refer to.
func (h *hub) setLocation(at string) {
	h.mu.Lock()
	h.at = at
	h.mu.Unlock()
}

// recv waits up to wait for messages from the user and hands them over.
func (h *hub) recv(ctx context.Context, wait time.Duration) ([]userMessage, error) {
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		h.mu.Lock()
		msgs := h.inbox
		h.inbox = nil
		wake := h.msgWake
		h.mu.Unlock()
		if len(msgs) > 0 {
			return msgs, nil
		}
		select {
		case <-wake:
		case <-timer.C:
			return nil, errNoMessage
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}
