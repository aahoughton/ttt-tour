# ttt-tour

Code tours in [ttt](https://github.com/eugenioenko/ttt). A coding agent opens
files, highlights lines and text, draws short notes under the code, and talks
with you in a **Tour** pane. You reply in the pane, so once you've asked for a
tour you can stay in ttt.

There are two parts, both in this repository:

- A ttt plugin (`plugin.ttt.json` and `init.lua`): the Tour pane with its
  Back and Next buttons, the highlights and notes, and the commands **Tour: Message Agent**,
  **Tour: Continue** and **Tour: End Tour**.
- `ttt-mcp`, an MCP server the agent runs over stdio. It listens on
  `127.0.0.1:4250`, and the plugin polls it for what to show and sends it
  what you type.

## What you need

- **Go 1.25 or later**, to build ttt-mcp (and ttt, below).
- **ttt built from its main branch.** The plugin uses plugin APIs that came
  after v1.8.0, and v1.8.0 can't load it. Until the next release:
  ```sh
  go install github.com/eugenioenko/ttt/cmd/ttt@main
  ```
  That puts `ttt` in `$(go env GOPATH)/bin`. If you also have a released ttt
  (from Homebrew, say), run this one by its full path or put that directory
  first on your PATH; `ttt --version` prints `dev` for this build.
- **An agent that runs MCP servers over stdio.** There are notes for
  [Claude Code](docs/claude-code.md), [Codex](docs/codex.md) and
  [pi](docs/pi.md).

## Setup

Clone this repository, build ttt-mcp and install the plugin:

```sh
git clone https://github.com/aahoughton/ttt-tour
cd ttt-tour
make build install-plugin
```

That gives you `bin/ttt-mcp` and copies the plugin into
`~/.config/ttt/plugins/ttt-tour`. (**Plugins: Install from URL** in ttt with
this repository's URL also installs the plugin.) The first time ttt loads the
plugin, it asks you to allow a bottom panel, commands, reading and writing the
editor, editor decorations, and HTTP to `127.0.0.1:4250` only. Choose
**Allow** (press `a`; Enter picks Deny). Deny removes the plugin, and
`make install-plugin` puts it back.

Register `bin/ttt-mcp` with your agent, using its absolute path: see the page
for [Claude Code](docs/claude-code.md), [Codex](docs/codex.md) or
[pi](docs/pi.md). Other MCP clients work like Codex: run `bin/ttt-mcp` with
no arguments, and let a tool call take at least 60 seconds.

## Taking a tour

1. Open the project in ttt.
2. Start your agent in the same directory. The agent starts ttt-mcp, and file
   paths in a tour resolve against the agent's directory.
3. Ask for a tour, for example "use ttt-tour to walk me through how config
   loading works", or use one of the [prompts](#prompts).
4. Read along in ttt and reply in the Tour pane. The pane appears with the
   first step, and steps keep your current focus; to reply, click the Tour
   input or run **Tour: Message Agent**. **Next >** (or Enter on an empty
   input) sends "continue" and **< Back** sends "back"; the agent decides
   what going back means. When the agent offers numbered choices, a button
   per number answers with that choice.
5. For a planned tour the agent posts an outline, and a menu at the end of
   the button row shows where you are ("2/5 ..."). Pick an entry with the
   mouse to jump there, or type "go to step 4". Wander off whenever you like:
   the agent shows asides without moving the marker, and revises the outline
   if the tour changes course.

## Rough edges

Agent tooling is still young, and some of that shows:

- One tour at a time. ttt-mcp needs port 4250, so a second agent session with
  ttt-tour has to wait for the first one to quit. Keep one ttt window open
  with the plugin, too: two would both act on the tour's steps.
- ttt has to come from main until the next release.
- Claude Code needs a long flag to let your replies reach it as you type them
  (see [its page](docs/claude-code.md)).
- Agents that use `wait_for_message` are busy while they wait for you, and some
  models stop waiting after the last step. If the agent doesn't answer, tell it
  in its own terminal to check the Tour pane.

## Troubleshooting

To check the plugin before a tour, run **Tour: Message Agent** in ttt; it
opens the Tour pane.

- **The pane says to start an agent, or the status reads "Waiting for
  ttt-mcp".** No agent with ttt-tour is running, or it hasn't started ttt-mcp
  yet. Check that the agent lists the ttt-tour server.
- **The agent says the plugin is not connected.** Open ttt (built from main)
  with the plugin installed and enabled.
- **The agent says `127.0.0.1:4250 is in use`.** Another agent session is
  running ttt-mcp. Quit it, and the next tool call takes over the port.
- **A step can't find a file.** Start the agent in the project's directory, or
  ask it for absolute paths.

## Prompts

| Prompt | Arguments | Use |
|--------|-----------|-----|
| `walk` | `TOPIC` | Guided tour of how something works |
| `branch` | `[BASE]` | Guided tour of what the current branch adds |
| `quiz` | `TOPIC` | Questions to check your understanding |
| `pair` | `TOPIC` | An exercise you write in ttt while the agent reviews it |

Claude Code shows these as `/mcp__ttt-tour__walk` and so on. Codex and pi
don't offer MCP prompts; ask for the same thing in your own words.

## Tools

| Tool | What it does |
|------|--------------|
| `show_code` | Opens a file, tints `start_line`..`end_line`, highlights each `highlight_text` entry, draws `note` under the range and adds `explanation` (markdown) to the Tour pane. `step` marks the outline entry it covers. Relative paths resolve against ttt-mcp's working directory. |
| `outline` | Sets or revises the tour's plan, a title per step, shown as a menu in the Tour pane. |
| `say` | Posts a markdown message to the Tour pane, with optional numbered `choices`, each with a button. |
| `move_pane` | Docks ttt's panel area at the `bottom` or `right` (saved as your layout), or brings the Tour pane to the front (`show`). |
| `get_editor_state` | Returns the active file, cursor, viewport and selection; with `include_text`, the whole buffer including unsaved edits. |
| `view_screen` | Returns the text on screen. |
| `end_tour` | Removes the highlights and notes and clears the pane. |
| `wait_for_message` | Without `--channel` only: waits up to 50 seconds for your next message. |

## Development

`make test` runs ttt-mcp's tests. `make install-plugin` copies the plugin
into ttt's config (`TTT_CONFIG_DIR=...` to change where); run it again after
a change, then **Plugins: Reload**. [docs/protocol.md](docs/protocol.md)
describes the HTTP protocol between the plugin and ttt-mcp.

## License

MIT
