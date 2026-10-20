package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// messageWait is how long wait_for_message waits before returning empty. It
// stays under the 60s tool-call timeout of clients such as Codex and pi, so a
// user still reading gets an empty result, not a timeout error. Agents do not
// choose it: given the choice, they pick waits the client times out. A
// variable so tests can shorten it.
var messageWait = 50 * time.Second

type tool struct {
	name        string
	description string
	schema      map[string]any
	run         func(ctx context.Context, args json.RawMessage) (string, error)
}

func object(required []string, props map[string]any) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func prop(typ, description string) map[string]any {
	return map[string]any{"type": typ, "description": description}
}

// decodeArgs rejects unknown fields so a misspelled argument fails loudly
// instead of being dropped.
func decodeArgs(args json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

func (s *server) instructions() string {
	var sb strings.Builder
	sb.WriteString(`Drives a running ttt editor, through its tour plugin, to explain code, specs
or documents to the user, quiz them, or work through exercises with them. The
user reads and replies in ttt's Tour pane.

- show_code opens a file, highlights a line range, draws a short note under
  it and adds the explanation (markdown) to the Tour pane. Prefer ranges that
  fit on one screen. highlight_text points at exact text inside the range (a
  parameter, a type, a phrase in a spec). Focus stays where the user left it.
- say posts a message to the Tour pane; with choices it is a numbered list
  with a button per number. A click sends "N. <choice>"; the user can also
  type an answer.
- outline sets the tour's plan, a short title per step, shown as a menu in
  the Tour pane. Set it before the first step of a planned tour and revise it
  whenever the conversation changes course; it is a plan you can change.
  Pass step to show_code to mark the entry a step covers; a step without it
  (an aside, an answer to a question) leaves the marker where it was. Picking
  an entry sends "go to step N: <title>".
- move_pane puts the pane at the bottom (wide code, long lines) or on the
  right (a running conversation beside the code). This changes the user's
  panel layout, so do it only when it clearly helps.
- get_editor_state tells you what the user is looking at or has selected,
  and with include_text returns the buffer, including unsaved edits.
  view_screen returns the whole screen as text.
- end_tour removes the highlights and clears the pane when the user is done.

Text you write in your own transcript never reaches the user: anything they
should see goes through say or show_code. Answer every user message with say
or show_code, even briefly; the pane shows "Waiting for the agent" until you do.
The pane has Back and Next buttons. "continue" (Next) means go on to the
next step. "back" (Back) means return to the previous step: usually show it
again, or recap it with say if that serves the user better.
`)
	if s.channel {
		sb.WriteString(`
Messages the user types in the Tour pane arrive as <channel source="ttt-tour"
at="file:lines">text</channel>; "at" is the step on screen when they sent it.
`)
	} else {
		sb.WriteString(`
After each reply, call wait_for_message to receive the user's next message
from the Tour pane. It returns without messages after 50 seconds; call it
again to keep waiting. Stop when the user asks to end the tour.
`)
	}
	return sb.String()
}

func (s *server) toolList() []tool {
	b := s.b
	tools := []tool{
		{
			name:        "show_code",
			description: "Open a file in ttt, highlight a line range, draw a note under it and add an explanation to the Tour pane. Show one step at a time and let the user reply before the next. The user reads only the Tour pane, not your own replies.",
			schema: object([]string{"file", "start_line"}, map[string]any{
				"file":        prop("string", "file path; absolute, or relative to the current directory"),
				"start_line":  prop("integer", "first line to highlight (1-based)"),
				"end_line":    prop("integer", "last line to highlight, inclusive; defaults to start_line"),
				"title":       prop("string", "short heading for this step"),
				"step":        prop("integer", "the outline entry this step covers (1-based); omit for an aside"),
				"explanation": prop("string", "markdown for the Tour pane: the main explanation of this step"),
				"note":        prop("string", "one or two plain-text sentences drawn under the highlighted lines; a blank line starts a new paragraph"),
				"highlight_text": map[string]any{
					"type":        "array",
					"description": "text within the range to highlight more strongly, e.g. a parameter, a type or a phrase in a spec",
					"items": object([]string{"line", "text"}, map[string]any{
						"line":       prop("integer", "line the text starts on (1-based)"),
						"text":       prop("string", "the exact text to highlight, copied from the line; may continue onto following lines with \n"),
						"occurrence": prop("integer", "which match on that line when the text appears more than once, counting matches that do not overlap (default 1)"),
					}),
				},
			}),
			run: b.showCode,
		},
		{
			name:        "say",
			description: "Post a message (markdown) to the user in ttt's Tour pane, optionally with numbered choices to answer. The user reads only the Tour pane, not your own replies, so answer every message from them with say or show_code.",
			schema: object([]string{"text"}, map[string]any{
				"text":    prop("string", "the message, in markdown"),
				"choices": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "options shown as a numbered list with a button each; the user answers in their next message"},
			}),
			run: b.say,
		},
		{
			name:        "outline",
			description: "Set or revise the tour's outline: a short title per step, shown as a menu in the Tour pane. Revise it freely as the tour changes course; mark progress with show_code's step.",
			schema: object([]string{"steps"}, map[string]any{
				"steps":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "a short title per step, in order"},
				"current": prop("integer", "the entry the user is on (1-based); 0 or omitted before the first"),
			}),
			run: b.setOutline,
		},
		{
			name:        "move_pane",
			description: "Dock ttt's panel area, which holds the Tour pane, at the bottom or on the right, or bring the Tour pane to the front. Docking is saved as the user's layout.",
			schema: object([]string{"position"}, map[string]any{
				"position": map[string]any{"type": "string", "enum": []string{"bottom", "right", "show"}},
			}),
			run: b.movePane,
		},
		{
			name:        "end_tour",
			description: "Remove the tour's highlights and notes from ttt and clear the Tour pane.",
			schema:      object(nil, map[string]any{}),
			run: func(ctx context.Context, _ json.RawMessage) (string, error) {
				if _, err := b.call(ctx, "clear", nil); err != nil {
					return "", err
				}
				b.setLocation("")
				b.mu.Lock()
				b.outline = outline{}
				b.mu.Unlock()
				return "Tour ended", nil
			},
		},
		{
			name:        "view_screen",
			description: "Return the text currently on the ttt screen.",
			schema:      object(nil, map[string]any{}),
			run:         b.viewScreen,
		},
		{
			name:        "get_editor_state",
			description: "Return the active file, cursor, visible lines and selected text in ttt (1-based lines and columns).",
			schema: object(nil, map[string]any{
				"include_text": prop("boolean", "also return the active buffer's full text, including unsaved edits"),
			}),
			run: func(ctx context.Context, args json.RawMessage) (string, error) {
				var in struct {
					IncludeText bool `json:"include_text"`
				}
				if err := decodeArgs(args, &in); err != nil {
					return "", err
				}
				data, err := b.call(ctx, "state", map[string]bool{"include_text": in.IncludeText})
				if err != nil {
					return "", err
				}
				var out bytes.Buffer
				if err := json.Indent(&out, data, "", "  "); err != nil {
					return "", err
				}
				return out.String(), nil
			},
		},
	}
	if !s.channel {
		for i := range tools {
			if tools[i].name == "show_code" || tools[i].name == "say" {
				tools[i].run = awaitReply(tools[i].run)
			}
		}
		tools = append(tools, tool{
			name:        "wait_for_message",
			description: "Wait for the user's next message from ttt's Tour pane. Call it after every say or show_code, and again when it returns no message; it returns empty after 50 seconds. Stop calling it when the user asks to end the tour.",
			schema:      object(nil, map[string]any{}),
			run:         b.waitForMessage,
		})
	}
	return tools
}

// awaitReply makes a successful result name the next call. Some clients never
// show the model the server instructions or follow tool descriptions loosely;
// the result is what the model reads right after showing the user something.
func awaitReply(run func(context.Context, json.RawMessage) (string, error)) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		out, err := run(ctx, args)
		if err != nil {
			return out, err
		}
		return out + ". Call wait_for_message for the user's reply before going on.", nil
	}
}

type span struct {
	Line       int    `json:"line"`
	Text       string `json:"text"`
	Occurrence int    `json:"occurrence,omitempty"`
}

// decoration is an item for the plugin's ttt.decorations.set: 1-based lines
// and rune columns, end_col exclusive.
type decoration struct {
	Line    int    `json:"line"`
	EndLine int    `json:"end_line,omitempty"`
	Col     int    `json:"col,omitempty"`
	EndCol  int    `json:"end_col,omitempty"`
	Note    string `json:"note,omitempty"`
}

func (b *hub) showCode(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		File        string `json:"file"`
		StartLine   int    `json:"start_line"`
		EndLine     int    `json:"end_line"`
		Title       string `json:"title"`
		Step        int    `json:"step"`
		Explanation string `json:"explanation"`
		Note        string `json:"note"`
		Highlights  []span `json:"highlight_text"`
	}
	if err := decodeArgs(args, &in); err != nil {
		return "", err
	}
	if in.File == "" {
		return "", fmt.Errorf("file is required")
	}
	var plan outline
	if in.Step != 0 {
		b.mu.Lock()
		plan = outline{Steps: b.outline.Steps, Current: in.Step}
		b.mu.Unlock()
		if in.Step < 1 || in.Step > len(plan.Steps) {
			return "", fmt.Errorf("step %d is not in the outline, which has %d steps; set it with outline first", in.Step, len(plan.Steps))
		}
	}
	end := max(in.EndLine, in.StartLine)
	if in.StartLine < 1 {
		return "", fmt.Errorf("start_line must be 1 or more")
	}
	path, err := filepath.Abs(in.File)
	if err != nil {
		return "", err
	}
	// ttt opens a missing path as a new, empty file, so check first.
	if fi, err := os.Stat(path); err != nil {
		return "", fmt.Errorf("cannot show %s: %w", in.File, err)
	} else if fi.IsDir() {
		return "", fmt.Errorf("cannot show %s: it is a directory", in.File)
	}
	data, err := b.call(ctx, "open", map[string]any{"path": path})
	if err != nil {
		return "", err
	}
	var opened struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(data, &opened); err != nil {
		return "", fmt.Errorf("reading %s from ttt: %w", in.File, err)
	}
	if end > len(opened.Lines) {
		return "", fmt.Errorf("%s has %d lines; cannot show %d-%d", in.File, len(opened.Lines), in.StartLine, end)
	}
	items := []decoration{{Line: in.StartLine, EndLine: end}}
	for _, sp := range in.Highlights {
		if sp.Line < in.StartLine || sp.Line > end {
			return "", fmt.Errorf("highlight_text line %d is outside lines %d-%d", sp.Line, in.StartLine, end)
		}
		d, err := spanDecoration(opened.Lines, sp)
		if err != nil {
			return "", err
		}
		items = append(items, d)
	}
	if note := strings.TrimSpace(in.Note); note != "" {
		items = append(items, decoration{Line: end, Note: note})
	}
	at := fmt.Sprintf("%s:%d-%d", displayPath(path), in.StartLine, end)
	if in.StartLine == end {
		at = fmt.Sprintf("%s:%d", displayPath(path), in.StartLine)
	}
	step := map[string]any{
		"path":     path,
		"line":     in.StartLine,
		"items":    items,
		"top":      max(1, in.StartLine-3),
		"markdown": stepMarkdown(in.Title, at, in.Explanation),
	}
	if in.Step != 0 {
		step["outline"] = plan
	}
	if _, err := b.call(ctx, "step", step); err != nil {
		return "", err
	}
	b.setLocation(at)
	if in.Step != 0 {
		b.mu.Lock()
		b.outline.Current = in.Step
		b.mu.Unlock()
	}
	return "Showing " + at, nil
}

func stepMarkdown(title, at, body string) string {
	var sb strings.Builder
	if title != "" {
		sb.WriteString("**" + title + "**  ")
	}
	sb.WriteString("`" + at + "`")
	if body = strings.TrimSpace(body); body != "" {
		sb.WriteString("\n\n" + body)
	}
	return sb.String()
}

// displayPath shortens path to one relative to the current directory when it
// is inside it.
func displayPath(path string) string {
	if wd, err := os.Getwd(); err == nil {
		if rel, err := filepath.Rel(wd, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return path
}

// spanDecoration finds sp.Text starting on line sp.Line, which it may run
// past onto the following lines, and returns it as a text range. Matches are
// counted without overlapping.
func spanDecoration(lines []string, sp span) (decoration, error) {
	if sp.Text == "" {
		return decoration{}, fmt.Errorf("highlight_text needs text")
	}
	if sp.Occurrence < 0 {
		return decoration{}, fmt.Errorf("occurrence must be 1 or more")
	}
	first := sp.Line - 1
	n := strings.Count(sp.Text, "\n") + 1
	if first < 0 || first+n > len(lines) {
		return decoration{}, fmt.Errorf("%q not found on line %d", sp.Text, sp.Line)
	}
	window := strings.Join(lines[first:first+n], "\n")
	start := -1
	for from, seen := 0, 0; from <= len(lines[first]); {
		i := strings.Index(window[from:], sp.Text)
		if i < 0 || from+i >= len(lines[first]) {
			break
		}
		seen++
		if seen == max(sp.Occurrence, 1) {
			start = from + i
			break
		}
		from += i + len(sp.Text)
	}
	if start < 0 {
		if sp.Occurrence > 1 {
			return decoration{}, fmt.Errorf("occurrence %d of %q not found on line %d", sp.Occurrence, sp.Text, sp.Line)
		}
		return decoration{}, fmt.Errorf("%q not found on line %d", sp.Text, sp.Line)
	}
	endLine, endCol := runePos(window, start+len(sp.Text))
	_, startCol := runePos(window, start)
	return decoration{Line: sp.Line, Col: startCol + 1, EndLine: first + endLine + 1, EndCol: endCol + 1}, nil
}

// runePos returns the 0-based line and rune column of byte offset off in
// text.
func runePos(text string, off int) (line, col int) {
	before := text[:off]
	line = strings.Count(before, "\n")
	if i := strings.LastIndexByte(before, '\n'); i >= 0 {
		before = before[i+1:]
	}
	return line, utf8.RuneCountInString(before)
}

// outline is the tour's plan as the Tour pane shows it. Current is the
// 1-based entry the user is on, or 0 before the first.
type outline struct {
	Steps   []string `json:"steps"`
	Current int      `json:"current"`
}

func (b *hub) setOutline(ctx context.Context, args json.RawMessage) (string, error) {
	var in outline
	if err := decodeArgs(args, &in); err != nil {
		return "", err
	}
	if len(in.Steps) == 0 {
		return "", fmt.Errorf("steps is required")
	}
	if in.Current < 0 || in.Current > len(in.Steps) {
		return "", fmt.Errorf("current must be between 0 and %d", len(in.Steps))
	}
	if _, err := b.call(ctx, "outline", in); err != nil {
		return "", err
	}
	b.mu.Lock()
	b.outline = in
	b.mu.Unlock()
	return fmt.Sprintf("Outline set: %d steps", len(in.Steps)), nil
}

func (b *hub) say(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Text    string   `json:"text"`
		Choices []string `json:"choices,omitempty"`
	}
	if err := decodeArgs(args, &in); err != nil {
		return "", err
	}
	text := strings.TrimSpace(in.Text)
	for i, c := range in.Choices {
		sep := "\n"
		if i == 0 {
			sep = "\n\n"
		}
		text += fmt.Sprintf("%s%d. %s", sep, i+1, c)
	}
	msg := map[string]any{"markdown": text}
	if len(in.Choices) > 0 {
		msg["choices"] = in.Choices
	}
	if _, err := b.call(ctx, "say", msg); err != nil {
		return "", err
	}
	return "Posted", nil
}

func (b *hub) movePane(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Position string `json:"position"`
	}
	if err := decodeArgs(args, &in); err != nil {
		return "", err
	}
	switch in.Position {
	case "bottom", "right", "show":
	default:
		return "", fmt.Errorf("position must be bottom, right or show")
	}
	if _, err := b.call(ctx, "pane", map[string]string{"where": in.Position}); err != nil {
		return "", err
	}
	return "Pane: " + in.Position, nil
}

func (b *hub) viewScreen(ctx context.Context, _ json.RawMessage) (string, error) {
	f, err := os.CreateTemp("", "ttt-mcp-screen-*")
	if err != nil {
		return "", err
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)
	if _, err := b.call(ctx, "screen", map[string]string{"path": path}); err != nil {
		return "", err
	}
	data, err := os.ReadFile(path)
	return string(data), err
}

func (b *hub) waitForMessage(ctx context.Context, args json.RawMessage) (string, error) {
	if err := decodeArgs(args, &struct{}{}); err != nil {
		return "", err
	}
	if err := b.listen(); err != nil {
		return "", err
	}
	msgs, err := b.recv(ctx, messageWait)
	if err == errNoMessage {
		return "No message yet. Call wait_for_message again to keep waiting.", nil
	}
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(msgs, "", "  ")
	return string(data), err
}
