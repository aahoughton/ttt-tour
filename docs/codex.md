# Codex

Register the server:

```sh
codex mcp add ttt-tour -- /absolute/path/to/bin/ttt-mcp
```

or add it to `~/.codex/config.toml`:

```toml
[mcp_servers.ttt-tour]
command = "/absolute/path/to/bin/ttt-mcp"
```

Codex waits for your messages with `wait_for_message`, which returns empty
after 50 seconds and is called again. That stays under Codex's default
`tool_timeout_sec` of 60. If you lower `tool_timeout_sec` for this server, keep
it above 50.

Codex asks before running each ttt-tour tool the first time; choose "Allow for
this session" so a tour does not stop at every step.

Codex does not offer MCP prompts as commands, so ask for a tour in your own
words, for example "use ttt-tour to walk me through how config loading works".
