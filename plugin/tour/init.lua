-- Code tours driven by an agent through ttt-mcp. ttt-mcp serves
-- 127.0.0.1:PORT; this plugin long-polls it for commands (open a file, show
-- a step, post a message, ...), reports each command's outcome, and sends
-- what the user types in the Tour pane.
local ttt = require("ttt")
local editor = require("ttt.editor")
local deco = require("ttt.decorations")
local net = require("ttt.net")
local json = require("ttt.json")

local PORT = 4250
local BASE = "http://127.0.0.1:" .. PORT
local POLL_WAIT = 25
local RETRY_MS = 2000

local thread = {}      -- { kind = "agent" | "user" | "step", text = markdown }
local acked = 0        -- last command carried out
local connected = false
local working = false  -- the user sent something the agent has not answered
local outbox = {}      -- messages not yet accepted by ttt-mcp
local sending = false
local session = tostring(os.time()) .. "-" .. tostring(math.random(1, 1000000))
local seq = 0

-- request calls done with { status = HTTP status or 0, body }. ttt.net
-- times out after 30 seconds, which bounds the long-poll wait.
local function request(path, body, done)
  local function finish(res)
    done({ status = res.status or 0, body = res.body or "" })
  end
  if body then
    net.post_async(BASE .. path, {
      headers = { ["Content-Type"] = "application/json" },
      body = body,
    }, finish)
  else
    net.get_async(BASE .. path, finish)
  end
end

local function add(kind, text)
  table.insert(thread, { kind = kind, text = text })
end

local function thread_markdown()
  if #thread == 0 then
    if connected then
      return "_Ask your agent for a tour, then reply here._"
    end
    return "_Waiting for ttt-mcp on port " .. PORT .. "._"
  end
  local parts = {}
  for i, e in ipairs(thread) do
    if e.kind == "step" and i > 1 then
      table.insert(parts, "---")
    end
    if e.kind == "user" then
      table.insert(parts, "**You:** " .. e.text:gsub("\n", "  \n"))
    else
      table.insert(parts, e.text)
    end
  end
  return table.concat(parts, "\n\n")
end

local function status()
  if not connected then
    return "Agent not connected"
  end
  if working then
    return "Agent is working..."
  end
  return " "
end

local function flush()
  if sending or #outbox == 0 then
    return
  end
  sending = true
  local msg = outbox[1]
  request("/message", json.encode(msg), function(res)
    sending = false
    if res.status == 204 or res.status == 200 then
      table.remove(outbox, 1)
      flush()
    else
      ttt.set_timeout(RETRY_MS, flush)
    end
  end)
end

local function send(text)
  text = text:gsub("^%s+", ""):gsub("%s+$", "")
  if text == "" then
    text = "continue"
  end
  seq = seq + 1
  table.insert(outbox, { id = session .. ":" .. seq, text = text })
  add("user", text)
  working = true
  flush()
end

local ops = {}

function ops.open(a)
  ttt.open_file(a.path)
  if editor.file_path() ~= a.path then
    error("cannot open " .. a.path .. " as text")
  end
  return { lines = editor.buffer_lines() }
end

function ops.step(a)
  deco.clear()
  deco.set(a.path, a.items)
  if editor.file_path() == a.path then
    editor.set_cursor(a.line, 1)
    editor.scroll_to(a.top)
  end
  add("step", a.markdown)
  working = false
  ttt.show_panel("bottom")
end

function ops.say(a)
  add("agent", a.markdown)
  working = false
  ttt.show_panel("bottom")
end

function ops.pane(a)
  if a.where == "right" then
    ttt.exec_command("panel.dockRight")
  elseif a.where == "bottom" then
    ttt.exec_command("panel.dockBottom")
  end
  ttt.show_panel("bottom")
end

function ops.clear()
  deco.clear()
  thread = {}
  working = false
end

function ops.state(a)
  local st = {
    file = editor.file_path(),
    cursor = editor.cursor(),
    viewport = editor.viewport(),
  }
  local sel = editor.selection()
  if sel.active then
    st.selection = sel
    st.selection.text = editor.selection_text()
  end
  if a.include_text then
    st.text = editor.buffer_text()
  end
  return st
end

function ops.screen(a)
  ttt.screenshot(a.path)
end

local function report(id, data, err)
  request("/result", json.encode({ id = id, data = data, error = err }), function() end)
end

local poll

local function handle(cmd)
  local op = ops[cmd.op]
  if not op then
    report(cmd.id, nil, "unknown command " .. tostring(cmd.op))
    return
  end
  local ok, result = pcall(op, cmd.args or {})
  if ok then
    report(cmd.id, result or {}, nil)
  else
    report(cmd.id, nil, (tostring(result):gsub("^[^:]*:%d+: ", "")))
  end
end

poll = function()
  request("/poll?after=" .. acked .. "&wait=" .. POLL_WAIT, nil, function(res)
    if res.status ~= 200 then
      if connected then
        connected = false
      end
      ttt.set_timeout(RETRY_MS, poll)
      return
    end
    connected = true
    local cmds = json.decode(res.body) or {}
    for _, cmd in ipairs(cmds) do
      if cmd.id > acked then
        handle(cmd)
        acked = cmd.id
      end
    end
    poll()
  end)
end

ttt.register({
  bottom = {
    title = "Tour",
    render = function(panel)
      panel:markdown({ text = thread_markdown(), follow = true })
      panel:label({ text = status(), style = "muted" })
      panel:input({
        prefix = "> ",
        placeholder = "Message the agent (Enter alone: continue; Alt+Enter or Ctrl+J: new line)",
        autofocus = true,
        wrap = true,
        max_rows = 6,
        clear_on_submit = true,
        on_submit = send,
      })
    end,
  },
  commands = {
    {
      id = "tour.message",
      title = "Tour: Message Agent",
      handler = function()
        ttt.focus_panel("bottom")
      end,
    },
    {
      id = "tour.continue",
      title = "Tour: Continue",
      handler = function()
        send("continue")
      end,
    },
    {
      id = "tour.end",
      title = "Tour: End Tour",
      handler = function()
        ops.clear()
      end,
    },
  },
})

ttt.set_timeout(100, poll)
