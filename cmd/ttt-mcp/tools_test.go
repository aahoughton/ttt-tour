package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakePlugin stands in for the ttt tour plugin: it long-polls the hub,
// records each command and answers it with respond.
type fakePlugin struct {
	mu      sync.Mutex
	cmds    []pluginCmd
	respond func(c pluginCmd) (any, string)
}

type pluginCmd struct {
	ID   int             `json:"id"`
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args"`
}

func (f *fakePlugin) ops() []pluginCmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]pluginCmd(nil), f.cmds...)
}

func (f *fakePlugin) run(ctx context.Context, base string) {
	acked := 0
	for ctx.Err() == nil {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/poll?after=%d&wait=1", base, acked), nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return
		}
		var cmds []pluginCmd
		json.NewDecoder(resp.Body).Decode(&cmds)
		resp.Body.Close()
		for _, c := range cmds {
			f.mu.Lock()
			f.cmds = append(f.cmds, c)
			f.mu.Unlock()
			var data any = map[string]any{}
			var errText string
			if f.respond != nil {
				data, errText = f.respond(c)
			}
			body, _ := json.Marshal(map[string]any{"id": c.ID, "data": data, "error": errText})
			if resp, err := http.Post(base+"/result", "application/json", bytes.NewReader(body)); err == nil {
				resp.Body.Close()
			}
			acked = c.ID
		}
	}
}

// client talks JSON-RPC to a server running over pipes.
type client struct {
	t      *testing.T
	in     *io.PipeWriter
	lines  chan json.RawMessage
	nextID int
	// notes collects notifications read while waiting for a reply.
	notes []json.RawMessage
}

// startServer runs ttt-mcp over pipes with plugin connected to its hub, or
// with nothing connected when plugin is nil.
func startServer(t *testing.T, plugin *fakePlugin, channel bool) (*client, string) {
	t.Helper()
	// show_code checks that the file exists; the fake plugin supplies its lines.
	t.Chdir(t.TempDir())
	for _, name := range []string{"a.go", "a.png", "main.go"} {
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	h := newHub()
	srv := httptest.NewServer(h.handler())
	t.Cleanup(srv.Close)
	if plugin != nil {
		ctx, cancel := context.WithCancel(context.Background())
		stopped := make(chan struct{})
		go func() {
			plugin.run(ctx, srv.URL)
			close(stopped)
		}()
		t.Cleanup(func() {
			cancel()
			<-stopped
		})
		for !h.connected() {
			time.Sleep(time.Millisecond)
		}
	}
	s := newServer(h, channel)
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan struct{})
	go func() {
		s.serve(context.Background(), inR, outW)
		outW.Close()
		close(done)
	}()
	c := &client{t: t, in: inW, lines: make(chan json.RawMessage, 64)}
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 64<<10), 16<<20)
		for sc.Scan() {
			c.lines <- json.RawMessage(append([]byte(nil), sc.Bytes()...))
		}
		close(c.lines)
	}()
	t.Cleanup(func() {
		inW.Close()
		<-done
	})
	c.request("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test"}})
	c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	return c, srv.URL
}

func (c *client) send(v any) {
	c.t.Helper()
	data, _ := json.Marshal(v)
	if _, err := c.in.Write(append(data, '\n')); err != nil {
		c.t.Fatal(err)
	}
}

func (c *client) next() json.RawMessage {
	c.t.Helper()
	select {
	case line, ok := <-c.lines:
		if !ok {
			c.t.Fatal("server closed its output")
		}
		return line
	case <-time.After(5 * time.Second):
		c.t.Fatal("no message from the server")
	}
	return nil
}

type reply struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func (c *client) request(method string, params any) reply {
	c.t.Helper()
	c.nextID++
	c.send(map[string]any{"jsonrpc": "2.0", "id": c.nextID, "method": method, "params": params})
	for {
		line := c.next()
		var r reply
		if err := json.Unmarshal(line, &r); err != nil {
			c.t.Fatal(err)
		}
		if r.Method != "" {
			c.notes = append(c.notes, line)
			continue
		}
		if r.ID != c.nextID {
			c.t.Fatalf("reply id %d, want %d", r.ID, c.nextID)
		}
		return r
	}
}

type callResult struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
}

func (c *client) call(name string, args map[string]any) (string, bool) {
	c.t.Helper()
	r := c.request("tools/call", map[string]any{"name": name, "arguments": args})
	if r.Error != nil {
		c.t.Fatalf("%s: %v", name, r.Error)
	}
	var res callResult
	if err := json.Unmarshal(r.Result, &res); err != nil {
		c.t.Fatal(err)
	}
	var sb strings.Builder
	for _, part := range res.Content {
		sb.WriteString(part.Text)
	}
	return sb.String(), res.IsError
}

// openFile answers open commands with lines, and everything else with {}.
func openFile(lines ...string) func(pluginCmd) (any, string) {
	return func(c pluginCmd) (any, string) {
		if c.Op == "open" {
			return map[string]any{"lines": lines}, ""
		}
		return map[string]any{}, ""
	}
}

func postMessage(t *testing.T, base, id, text string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"id": id, "text": text})
	resp, err := http.Post(base+"/message", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestInitializeAdvertisesChannelOnlyInChannelMode(t *testing.T) {
	for _, channel := range []bool{false, true} {
		c, _ := startServer(t, nil, channel)
		r := c.request("initialize", map[string]any{"protocolVersion": "1999-01-01"})
		var res struct {
			ProtocolVersion string `json:"protocolVersion"`
			Capabilities    struct {
				Experimental map[string]any `json:"experimental"`
			} `json:"capabilities"`
			Instructions string `json:"instructions"`
		}
		json.Unmarshal(r.Result, &res)
		if res.ProtocolVersion != defaultProtocolVersion {
			t.Errorf("protocol = %q for an unknown client version", res.ProtocolVersion)
		}
		if _, ok := res.Capabilities.Experimental["claude/channel"]; ok != channel {
			t.Errorf("channel=%v: claude/channel capability present = %v", channel, ok)
		}
		if strings.Contains(res.Instructions, "wait_for_message") == channel {
			t.Errorf("channel=%v: instructions mention wait_for_message = %v", channel, !channel)
		}
		lr := c.request("tools/list", nil)
		if strings.Contains(string(lr.Result), `"wait_for_message"`) == channel {
			t.Errorf("channel=%v: wait_for_message listed = %v", channel, !channel)
		}
	}
}

func TestShowCodeOpensTheFileThenSendsTheStep(t *testing.T) {
	plugin := &fakePlugin{respond: openFile("package main", "", "func main() {", "\tfmt.Println(\"héllo\")", "}")}
	c, _ := startServer(t, plugin, false)
	out, isErr := c.call("show_code", map[string]any{
		"file": "main.go", "start_line": 3, "end_line": 5, "title": "Entry", "explanation": "Runs **once**.",
		"note":           "The program starts here.",
		"highlight_text": []map[string]any{{"line": 4, "text": "\"héllo\")\n}"}},
	})
	if isErr || out != "Showing main.go:3-5. Call wait_for_message for the user's reply before going on." {
		t.Fatalf("show_code = %q, isErr %v", out, isErr)
	}
	ops := plugin.ops()
	if len(ops) != 2 || ops[0].Op != "open" || ops[1].Op != "step" {
		t.Fatalf("ops = %+v, want open then step", ops)
	}
	wd, _ := os.Getwd()
	var open struct {
		Path string `json:"path"`
	}
	json.Unmarshal(ops[0].Args, &open)
	if open.Path != filepath.Join(wd, "main.go") {
		t.Errorf("open = %+v", open)
	}
	var step struct {
		Path     string       `json:"path"`
		Line     int          `json:"line"`
		Items    []decoration `json:"items"`
		Top      int          `json:"top"`
		Markdown string       `json:"markdown"`
	}
	json.Unmarshal(ops[1].Args, &step)
	want := []decoration{
		{Line: 3, EndLine: 5},
		{Line: 4, Col: 14, EndLine: 5, EndCol: 2},
		{Line: 5, Note: "The program starts here."},
	}
	if !reflect.DeepEqual(step.Items, want) {
		t.Errorf("items = %+v, want %+v", step.Items, want)
	}
	if step.Path != open.Path || step.Line != 3 || step.Top != 1 || step.Markdown != "**Entry**  `main.go:3-5`\n\nRuns **once**." {
		t.Errorf("step = %+v", step)
	}
}

func TestShowCodeRejectsBadRangesAndText(t *testing.T) {
	plugin := &fakePlugin{respond: openFile("a := 1", "b := 2")}
	c, _ := startServer(t, plugin, false)
	for _, tc := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"file": "a.go", "start_line": 2, "end_line": 3}, "a.go has 2 lines; cannot show 2-3"},
		{map[string]any{"file": "a.go", "start_line": 1, "highlight_text": []map[string]any{{"line": 2, "text": "b"}}}, "outside lines 1-1"},
		{map[string]any{"file": "a.go", "start_line": 1, "highlight_text": []map[string]any{{"line": 1, "text": "c"}}}, `"c" not found on line 1`},
		{map[string]any{"file": "a.go", "start_line": 0}, "start_line must be 1 or more"},
		{map[string]any{"file": "a.go", "start_line": 1, "bogus": true}, "unknown field"},
		{map[string]any{"file": "missing.go", "start_line": 1}, "cannot show missing.go"},
		{map[string]any{"file": ".", "start_line": 1}, "it is a directory"},
	} {
		out, isErr := c.call("show_code", tc.args)
		if !isErr || !strings.Contains(out, tc.want) {
			t.Errorf("%v: %q (isErr %v), want %q", tc.args, out, isErr, tc.want)
		}
	}
	for _, op := range plugin.ops() {
		if op.Op == "step" {
			t.Fatal("a rejected step reached the plugin")
		}
	}
}

func TestOutlineAndStepsThatMarkIt(t *testing.T) {
	plugin := &fakePlugin{respond: openFile("a := 1", "b := 2")}
	c, _ := startServer(t, plugin, false)
	if out, isErr := c.call("show_code", map[string]any{"file": "a.go", "start_line": 1, "step": 1}); !isErr || !strings.Contains(out, "set it with outline first") {
		t.Fatalf("step before outline = %q, isErr %v", out, isErr)
	}
	if out, isErr := c.call("outline", map[string]any{"steps": []string{"One", "Two"}}); isErr {
		t.Fatal(out)
	}
	if out, isErr := c.call("show_code", map[string]any{"file": "a.go", "start_line": 2, "step": 2}); isErr {
		t.Fatal(out)
	}
	if out, isErr := c.call("show_code", map[string]any{"file": "a.go", "start_line": 1}); isErr {
		t.Fatal(out)
	}
	if out, isErr := c.call("show_code", map[string]any{"file": "a.go", "start_line": 1, "step": 3}); !isErr || !strings.Contains(out, "step 3 is not in the outline") {
		t.Fatalf("step 3 = %q, isErr %v", out, isErr)
	}
	var got []string
	for _, op := range plugin.ops() {
		if op.Op == "outline" || op.Op == "step" {
			var args struct {
				Outline json.RawMessage `json:"outline"`
			}
			json.Unmarshal(op.Args, &args)
			got = append(got, op.Op+" "+string(args.Outline))
		}
	}
	want := []string{
		"outline ",
		`step {"steps":["One","Two"],"current":2}`,
		"step ",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ops = %q, want %q", got, want)
	}
	if outline := plugin.ops()[0]; outline.Op != "outline" || string(outline.Args) != `{"steps":["One","Two"],"current":0}` {
		t.Fatalf("first op = %s %s", outline.Op, outline.Args)
	}
}

func TestSpanDecorationCountsMatchesWithoutOverlap(t *testing.T) {
	lines := []string{"x == y == z ==== w", "世界 ab ab"}
	for _, tc := range []struct {
		sp   span
		want decoration
	}{
		{span{Line: 1, Text: "==", Occurrence: 4}, decoration{Line: 1, Col: 15, EndLine: 1, EndCol: 17}},
		{span{Line: 2, Text: "ab", Occurrence: 2}, decoration{Line: 2, Col: 7, EndLine: 2, EndCol: 9}},
	} {
		got, err := spanDecoration(lines, tc.sp)
		if err != nil || got != tc.want {
			t.Errorf("%+v: %+v %v, want %+v", tc.sp, got, err, tc.want)
		}
	}
	if _, err := spanDecoration(lines, span{Line: 1, Text: "==", Occurrence: 5}); err == nil || !strings.Contains(err.Error(), "occurrence 5") {
		t.Errorf("err = %v", err)
	}
}

func TestSayAndMovePane(t *testing.T) {
	plugin := &fakePlugin{}
	c, _ := startServer(t, plugin, false)
	if out, isErr := c.call("say", map[string]any{"text": "Which one?", "choices": []string{"a", "b"}}); isErr {
		t.Fatal(out)
	}
	if out, isErr := c.call("move_pane", map[string]any{"position": "right"}); isErr {
		t.Fatal(out)
	}
	if out, isErr := c.call("move_pane", map[string]any{"position": "left"}); !isErr {
		t.Fatalf("move_pane left = %q, want an error", out)
	}
	ops := plugin.ops()
	if len(ops) != 2 || string(ops[0].Args) != `{"choices":["a","b"],"markdown":"Which one?\n\n1. a\n2. b"}` || string(ops[1].Args) != `{"where":"right"}` {
		t.Fatalf("ops = %+v", ops)
	}
}

func TestPluginErrorsReachTheAgent(t *testing.T) {
	c, _ := startServer(t, &fakePlugin{respond: func(pluginCmd) (any, string) { return nil, "not a text file" }}, false)
	out, isErr := c.call("show_code", map[string]any{"file": "a.png", "start_line": 1})
	if !isErr || out != "ttt: not a text file" {
		t.Fatalf("show_code = %q, isErr %v", out, isErr)
	}
}

func TestTakenPortIsReportedAndRetried(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	h := newHub()
	h.addr = ln.Addr().String()
	for _, call := range []func() error{
		func() error { _, err := h.call(context.Background(), "say", nil); return err },
		func() error { _, err := h.waitForMessage(context.Background(), json.RawMessage(`{}`)); return err },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), h.addr+" is in use") {
			t.Fatalf("err = %v, want %s is in use", err, h.addr)
		}
	}
	ln.Close()
	if _, err := h.call(context.Background(), "say", nil); err != errNotConnected {
		t.Fatalf("after the port frees up, err = %v, want errNotConnected", err)
	}
	resp, err := http.Get("http://" + h.addr + "/poll?wait=0")
	if err != nil {
		t.Fatalf("hub does not serve the plugin after taking over the port: %v", err)
	}
	resp.Body.Close()
}

func TestToolsFailFastWithoutThePlugin(t *testing.T) {
	c, _ := startServer(t, nil, false)
	out, isErr := c.call("say", map[string]any{"text": "hi"})
	if !isErr || !strings.Contains(out, "tour plugin is not connected") {
		t.Fatalf("say = %q, isErr %v", out, isErr)
	}
}

func TestViewScreenReadsWhatThePluginWrote(t *testing.T) {
	plugin := &fakePlugin{respond: func(c pluginCmd) (any, string) {
		var args struct{ Path string }
		json.Unmarshal(c.Args, &args)
		os.WriteFile(args.Path, []byte("SCREEN"), 0o600)
		return map[string]any{}, ""
	}}
	c, _ := startServer(t, plugin, false)
	if out, isErr := c.call("view_screen", map[string]any{}); isErr || out != "SCREEN" {
		t.Fatalf("view_screen = %q, isErr %v", out, isErr)
	}
}

func TestWaitForMessageReturnsEachMessageOnceWithItsStep(t *testing.T) {
	plugin := &fakePlugin{respond: openFile("a", "b")}
	c, base := startServer(t, plugin, false)
	c.call("show_code", map[string]any{"file": "a.go", "start_line": 2})
	postMessage(t, base, "s1:1", "why?")
	postMessage(t, base, "s1:1", "why?")
	postMessage(t, base, "s1:2", "continue")
	out, isErr := c.call("wait_for_message", map[string]any{})
	if isErr {
		t.Fatal(out)
	}
	var msgs []userMessage
	json.Unmarshal([]byte(out), &msgs)
	want := []userMessage{{Text: "why?", At: "a.go:2"}, {Text: "continue", At: "a.go:2"}}
	if !reflect.DeepEqual(msgs, want) {
		t.Fatalf("messages = %+v, want %+v", msgs, want)
	}
}

func TestWaitForMessageTimesOutQuietly(t *testing.T) {
	defer func(d time.Duration) { messageWait = d }(messageWait)
	messageWait = 100 * time.Millisecond
	c, _ := startServer(t, nil, false)
	out, isErr := c.call("wait_for_message", map[string]any{})
	if isErr || !strings.Contains(out, "No message yet") {
		t.Fatalf("wait = %q, isErr %v", out, isErr)
	}
}

func TestSayPointsToWaitForMessageOnlyWithoutChannel(t *testing.T) {
	for _, channel := range []bool{false, true} {
		c, _ := startServer(t, &fakePlugin{}, channel)
		out, isErr := c.call("say", map[string]any{"text": "hi"})
		if isErr || strings.Contains(out, "wait_for_message") == channel {
			t.Errorf("channel=%v: say = %q, isErr %v", channel, out, isErr)
		}
	}
}

func TestChannelModeRelaysMessagesAsNotifications(t *testing.T) {
	c, base := startServer(t, nil, true)
	postMessage(t, base, "s:1", "go on")
	var n reply
	json.Unmarshal(c.next(), &n)
	if n.Method != "notifications/claude/channel" {
		t.Fatalf("unexpected message %+v", n)
	}
	var p map[string]any
	json.Unmarshal(n.Params, &p)
	if p["content"] != "go on" || len(p["meta"].(map[string]any)) != 0 {
		t.Errorf("notification = %v", p)
	}
}

func TestUnacknowledgedCommandsAreSentAgain(t *testing.T) {
	h := newHub()
	srv := httptest.NewServer(h.handler())
	defer srv.Close()
	poll := func(after int) []pluginCmd {
		resp, err := http.Get(fmt.Sprintf("%s/poll?after=%d&wait=1", srv.URL, after))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var cmds []pluginCmd
		json.NewDecoder(resp.Body).Decode(&cmds)
		return cmds
	}
	poll(0)
	go h.call(context.Background(), "say", nil)
	var first []pluginCmd
	for len(first) == 0 {
		first = poll(0)
	}
	if again := poll(0); len(again) != 1 || again[0].ID != first[0].ID {
		t.Fatalf("second poll = %+v, want the unacknowledged command again", again)
	}
	if after := poll(first[0].ID); len(after) != 0 {
		t.Fatalf("poll after ack = %+v, want nothing", after)
	}
}

func TestPromptsRenderWithArguments(t *testing.T) {
	c, _ := startServer(t, nil, false)
	r := c.request("prompts/get", map[string]any{"name": "quiz", "arguments": map[string]string{"topic": "the renderer"}})
	if r.Error != nil || !strings.Contains(string(r.Result), "Quiz me on the renderer") {
		t.Fatalf("prompt = %s %v", r.Result, r.Error)
	}
	if r := c.request("prompts/get", map[string]any{"name": "walk"}); r.Error == nil {
		t.Fatal("walk without a topic did not fail")
	}
}

func TestDuplicateInFlightIDIsRejected(t *testing.T) {
	c, base := startServer(t, nil, false)
	for range 2 {
		c.send(map[string]any{"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": map[string]any{"name": "wait_for_message", "arguments": map[string]any{}}})
	}
	var r reply
	json.Unmarshal(c.next(), &r)
	if r.ID != 7 || r.Error == nil || r.Error.Code != codeInvalidRequest {
		t.Fatalf("reply = %+v, want the duplicate rejected", r)
	}
	postMessage(t, base, "x", "hi")
	var first reply
	json.Unmarshal(c.next(), &first)
	if first.ID != 7 || first.Error != nil {
		t.Fatalf("reply = %+v, want the first request answered", first)
	}
}

func TestUnknownMethodIsAnError(t *testing.T) {
	c, _ := startServer(t, nil, false)
	if r := c.request("resources/list", nil); r.Error == nil || r.Error.Code != codeMethodNotFound {
		t.Fatalf("reply = %+v", r)
	}
}

func TestCancelledRequestGetsNoReply(t *testing.T) {
	c, _ := startServer(t, nil, false)
	c.send(map[string]any{"jsonrpc": "2.0", "id": 99, "method": "tools/call", "params": map[string]any{"name": "wait_for_message", "arguments": map[string]any{}}})
	c.send(map[string]any{"jsonrpc": "2.0", "method": "notifications/cancelled", "params": map[string]any{"requestId": 99}})
	if r := c.request("ping", nil); r.Error != nil {
		t.Fatalf("ping = %+v", r)
	}
}

func TestCommandIDsKeepIncreasingAcrossRestarts(t *testing.T) {
	first := newHub()
	time.Sleep(2 * time.Millisecond)
	if next := newHub().nextID; next <= first.nextID {
		t.Fatalf("a new hub starts at %d, at or below the previous hub's %d", next, first.nextID)
	}
}
