package main

import (
	"fmt"
	"slices"
	"strings"
)

type promptDef struct {
	name, description string
	arg, argDesc      string
	argRequired       bool
	text              func(arg string) string
}

const pacing = `Pace it as a conversation in ttt's Tour pane: one step per show_code, then
wait for me. "continue" means the next step and "back" the previous one;
anything else is a question to answer first, showing other code if that helps.`

var prompts = []promptDef{
	{
		name: "walk", description: "Guided tour of how some code (or a spec or document) works",
		arg: "topic", argDesc: "what to explain, e.g. a feature, file or flow", argRequired: true,
		text: func(topic string) string {
			return fmt.Sprintf(`Walk me through %s in ttt. Read the code first, then plan a short sequence
of steps in a logical order, each a range that fits on one screen.

%s

Don't quiz me unless I ask; at most offer an occasional optional question.
End with a short summary using say.`, topic, pacing)
		},
	},
	{
		name: "branch", description: "Guided tour of what the current branch adds",
		arg: "base", argDesc: "base to compare against (default: the merge base with the default branch)",
		text: func(base string) string {
			if base == "" {
				base = "the merge base with the default branch"
			}
			return fmt.Sprintf(`Explain what the current branch adds compared to %s. Read the commits and
the diff yourself first. Then walk me through the change in a logical order
rather than file order: the goal, the core change, then supporting pieces and
tests. Show the new code with show_code on the working tree files.

%s

Keep it light: no quizzing unless I ask. End with a short summary of what to
look at closely in review.`, base, pacing)
		},
	},
	{
		name: "quiz", description: "Check my understanding of a topic with questions",
		arg: "topic", argDesc: "what to quiz on", argRequired: true,
		text: func(topic string) string {
			return fmt.Sprintf(`Quiz me on %s to check that I understand it. Ask one question at a time
with say (choices for multiple choice, otherwise free text), and show the
relevant code with show_code when the question is about code. Grade each
answer honestly: say what was right, correct what wasn't, and point at the
code. Adjust the difficulty and come back to weak spots. When I say stop,
summarize what I know well and what to review.`, topic)
		},
	},
	{
		name: "pair", description: "Hands-on exercise: I write code in ttt, you review and correct",
		arg: "topic", argDesc: "what to practice", argRequired: true,
		text: func(topic string) string {
			return fmt.Sprintf(`Let's work through a hands-on exercise on %s. Create a small exercise in a
scratch file in the workspace, open it with show_code, and explain the goal
with say. Then I write the code in ttt. When I say I'm done or ask for help,
read my buffer with get_editor_state include_text (it has unsaved edits) and
review it: point at problems with show_code on the exact lines, and give
hints before full answers. If I ask you to change the file, ask me to save
first, then edit it and show me what changed.`, topic)
		},
	},
}

func promptList() []map[string]any {
	list := make([]map[string]any, len(prompts))
	for i, p := range prompts {
		list[i] = map[string]any{
			"name":        p.name,
			"description": p.description,
			"arguments":   []map[string]any{{"name": p.arg, "description": p.argDesc, "required": p.argRequired}},
		}
	}
	return list
}

func getPrompt(name string, args map[string]string) (any, error) {
	i := slices.IndexFunc(prompts, func(p promptDef) bool { return p.name == name })
	if i < 0 {
		return nil, &rpcError{Code: codeInvalidParams, Message: "unknown prompt " + name}
	}
	p := prompts[i]
	arg := strings.TrimSpace(args[p.arg])
	if arg == "" && p.argRequired {
		return nil, &rpcError{Code: codeInvalidParams, Message: "missing argument " + p.arg}
	}
	return map[string]any{
		"description": p.description,
		"messages": []map[string]any{{
			"role":    "user",
			"content": map[string]any{"type": "text", "text": p.text(arg)},
		}},
	}, nil
}
