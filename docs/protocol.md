# Plugin protocol

ttt-mcp serves loopback HTTP for the plugin:

- `GET /poll?after=ID&wait=S` returns the queued commands, `[{id, op, args}]`,
  waiting up to `S` seconds for one. `after` acknowledges the commands up to
  `ID`; unacknowledged ones are sent again, so a reloaded plugin replays rather
  than loses them. IDs increase across ttt-mcp restarts.
- `POST /result {id, data, error}` reports a command's outcome.
- `POST /message {id, text}` sends what the user typed; `id` makes retries
  safe.

Ops: `open {path}` returns `{lines}`; `step {path, line, top, items,
markdown}`; `say {markdown, choices}` (choices optional); `pane {where}`; `clear`; `state {include_text}`;
`screen {path}`.

Only one ttt-mcp can listen on the port at a time (`--port` changes it, but the
plugin and its manifest use 4250).
