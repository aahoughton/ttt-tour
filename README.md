# ttt-tour

Code tours in [ttt](https://github.com/eugenioenko/ttt): a coding agent opens
files, highlights lines and text, draws short notes under the code, and talks
with you in a **Tour** pane. You reply in the pane, so once you have asked for a
tour you can stay in ttt.

Two parts, both in this repository:

- A ttt plugin (`plugin.ttt.json` and `init.lua` at the root): the Tour pane,
  the highlights and notes, and the commands **Tour: Message Agent**,
  **Tour: Continue** and **Tour: End Tour**.
- `cmd/ttt-mcp`, an MCP server over stdio for the agent. It listens on
  `127.0.0.1:4250`; the plugin long-polls it for what to show and posts what you
  type.

## Requirements

A ttt newer than v1.8.0. The plugin needs `ttt.show_panel` /
`ttt.focus_panel` and markdown `follow` (eugenioenko/ttt#991), which are on
ttt's main branch and not yet in a release, so for now build ttt from main.

An agent that can use MCP servers over stdio.

## Setup

In ttt, run **Plugins: Install from URL** with this repository's URL, and allow
the plugin when it asks: a bottom panel, commands, reading and writing the
editor, editor decorations, and HTTP to `127.0.0.1:4250` (ttt-mcp) only.

Build ttt-mcp:

```sh
make build            # bin/ttt-mcp
```

Register `bin/ttt-mcp` with your agent:

- [Claude Code](docs/claude-code.md)
- [Codex](docs/codex.md)
- [pi](docs/pi.md)

Other MCP clients work the same way as Codex: run `bin/ttt-mcp` with no
arguments, and allow a tool call to take at least 60 seconds.

Ask for a tour, or use the prompts below, then read and reply in the Tour pane.
Each step leaves the focus in the editor so you can move around the code;
click the Tour input or run **Tour: Message Agent** to reply. Enter on an empty
input sends "continue".

## Prompts

| Prompt | Arguments | Use |
|--------|-----------|-----|
| `walk` | `TOPIC` | Guided tour of how something works |
| `branch` | `[BASE]` | Guided tour of what the current branch adds |
| `quiz` | `TOPIC` | Questions to check your understanding |
| `pair` | `TOPIC` | An exercise you write in ttt while the agent reviews it |

Claude Code shows these as `/mcp__ttt-tour__walk` and so on. Agents that do
not support MCP prompts, such as Codex, skip them; ask for the same thing in
your own words.

## Tools

| Tool | What it does |
|------|--------------|
| `show_code` | Opens a file, tints `start_line`..`end_line`, highlights each `highlight_text` entry, draws `note` under the range and adds `explanation` (markdown) to the Tour pane. Relative paths resolve against ttt-mcp's working directory. |
| `say` | Posts a markdown message to the Tour pane, with optional numbered `choices`. |
| `move_pane` | Docks ttt's panel area at the `bottom` or `right` (saved as your layout), or brings the Tour pane to the front (`show`). |
| `get_editor_state` | Returns the active file, cursor, viewport and selection; with `include_text`, the whole buffer including unsaved edits. |
| `view_screen` | Returns the text on screen. |
| `end_tour` | Removes the highlights and notes and clears the pane. |
| `wait_for_message` | Without `--channel` only: waits up to 50 seconds for your next message. |

## Development

`make install-plugin` copies the plugin into `~/.config/ttt/plugins/ttt-tour`
(`TTT_CONFIG_DIR=...` to change); run it again after a change, then
**Plugins: Reload**. `make test` runs ttt-mcp's tests.

[docs/protocol.md](docs/protocol.md) describes the HTTP protocol between the
plugin and ttt-mcp.
