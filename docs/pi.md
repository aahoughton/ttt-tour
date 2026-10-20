# pi

pi 0.99 and later connect to MCP servers without an extension. Add the server
to `~/.pi/agent/mcp.json` (or `.pi/mcp.json` in a trusted project):

```json
{
  "mcpServers": {
    "ttt-tour": {
      "command": "/absolute/path/to/bin/ttt-mcp",
      "exposure": "direct"
    }
  }
}
```

`pi mcp add ttt-tour -- /absolute/path/to/bin/ttt-mcp` writes the same entry
without `exposure`. Set `"exposure": "direct"` so the tour tools are declared
to the model like built-in tools; under the default, `codemode`, the model
reaches them only through scripts. `pi mcp list` checks the connection.

pi does not show ttt-mcp's server instructions to the model for `direct`
tools, and its built-in MCP support does not offer MCP prompts, so ask for a
tour in your own words, for example "use ttt-tour to walk me through how config
loading works". The tool descriptions and results tell the model to wait for
your replies. Some models still end their turn after the last step; what you
type in the Tour pane then waits in ttt-mcp until you ask pi, in its own
terminal, to read it.

## pi-mcp-adapter

If you use [pi-mcp-adapter](https://github.com/nicobailon/pi-mcp-adapter)
instead, it replaces pi's built-in MCP support and offers MCP prompts as slash
commands. Like the built-in, it keeps the server instructions out of the model's
context. Keep ttt-mcp running for the session, since it holds the tour's
state, and declare its tools directly:

```json
{
  "mcpServers": {
    "ttt-tour": {
      "command": "/absolute/path/to/bin/ttt-mcp",
      "lifecycle": "keep-alive",
      "directTools": true
    }
  }
}
```
