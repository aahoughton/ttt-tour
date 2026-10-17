# ttt-tour

Code tours in [ttt](https://github.com/eugenioenko/ttt): an agent such as Claude
Code opens files, highlights lines and text, draws short notes under the code,
and talks with you in a **Tour** pane. You reply in the pane, so once you have
asked for a tour you can stay in ttt.

Two parts:

- `plugin/tour`, a ttt plugin: the Tour pane, the highlights and notes, and the
  commands **Tour: Message Agent**, **Tour: Continue** and **Tour: End Tour**.
- `cmd/ttt-mcp`, an MCP server over stdio for the agent. It listens on
  `127.0.0.1:4250`; the plugin long-polls it for what to show and posts what you
  type.

## Requirements

A ttt with the plugin APIs this needs: `ttt.decorations` (line tints, text
highlights and notes; eugenioenko/ttt#952) and `ttt.show_panel` /
`ttt.focus_panel`, input `autofocus` and markdown `follow`. Until those land,
build ttt from a branch that has them.

`curl` on the PATH. ttt's plugin network API refuses loopback addresses, so the
plugin reaches ttt-mcp through `curl` (the plugin asks for `system.exec: curl`).

## Setup

```sh
make build            # bin/ttt-mcp
make install-plugin   # copies plugin/tour into ~/.config/ttt/plugins (TTT_CONFIG_DIR=... to change)
```

Start ttt and allow the plugin when it asks. Then register the server with
Claude Code, in the project you want explained:

```sh
claude mcp add ttt-tour -- /absolute/path/to/bin/ttt-mcp --channel
claude --dangerously-load-development-channels server:ttt-tour
```

`--channel` pushes what you type in the Tour pane into the Claude session as a
channel notification; Claude Code shows a confirmation for development
channels. Without it, ttt-mcp offers a `wait_for_message` tool instead, which
works with any MCP client but keeps the agent busy waiting between messages.
Use one mode or the other for a server name: a `--channel` server in a session
started without the channel flag still shows code, but your messages do not
reach the agent.

Ask for a tour in Claude, or with the prompts below, then read and reply in
the Tour pane. Enter on an empty input sends "continue".

## Prompts

| Prompt | Use |
|--------|-----|
| `/mcp__ttt-tour__walk TOPIC` | Guided tour of how something works |
| `/mcp__ttt-tour__branch [BASE]` | Guided tour of what the current branch adds |
| `/mcp__ttt-tour__quiz TOPIC` | Questions to check your understanding |
| `/mcp__ttt-tour__pair TOPIC` | An exercise you write in ttt while the agent reviews it |

## Tools

| Tool | What it does |
|------|--------------|
| `show_code` | Opens a file, tints `start_line`..`end_line`, highlights each `highlight_text` entry, draws `note` under the range and adds `explanation` (markdown) to the Tour pane. Relative paths resolve against ttt-mcp's working directory. |
| `say` | Posts a markdown message to the Tour pane, with optional numbered `choices`. |
| `move_pane` | Docks ttt's panel area at the `bottom` or `right` (saved as your layout), or brings the Tour pane to the front (`show`). |
| `get_editor_state` | Returns the active file, cursor, viewport and selection; with `include_text`, the whole buffer including unsaved edits. |
| `view_screen` | Returns the text on screen. |
| `end_tour` | Removes the highlights and notes and clears the pane. |
| `wait_for_message` | Without `--channel` only: waits for your next message. |

## Protocol

ttt-mcp serves loopback HTTP for the plugin:

- `GET /poll?after=ID&wait=S` returns the queued commands, `[{id, op, args}]`,
  waiting up to `S` seconds for one. `after` acknowledges the commands up to
  `ID`; unacknowledged ones are sent again, so a reloaded plugin replays rather
  than loses them. IDs increase across ttt-mcp restarts.
- `POST /result {id, data, error}` reports a command's outcome.
- `POST /message {id, text}` sends what the user typed; `id` makes retries
  safe.

Ops: `open {path}` returns `{lines}`; `step {path, line, top, items,
markdown}`; `say {markdown}`; `pane {where}`; `clear`; `state {include_text}`;
`screen {path}`.

Only one ttt-mcp can listen on the port at a time (`--port` changes it, but the
plugin uses 4250).
