# Claude Code

Register the server in the project you want explained, and start Claude with
the channel enabled:

```sh
claude mcp add ttt-tour -- /absolute/path/to/bin/ttt-mcp --channel
claude --dangerously-load-development-channels server:ttt-tour
```

`--channel` pushes what you type in the Tour pane into the Claude session as a
channel notification; Claude Code shows a confirmation for development
channels.

Without `--channel`, ttt-mcp offers a `wait_for_message` tool instead, as it
does for other agents. That needs no extra flag but keeps Claude busy waiting
between messages:

```sh
claude mcp add ttt-tour -- /absolute/path/to/bin/ttt-mcp
```

Use one mode or the other for a server name: a `--channel` server in a session
started without the channel flag still shows code, but your messages do not
reach Claude.

The prompts appear as `/mcp__ttt-tour__walk`, `/mcp__ttt-tour__branch`,
`/mcp__ttt-tour__quiz` and `/mcp__ttt-tour__pair`.
