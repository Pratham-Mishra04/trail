# trail — Claude Code plugin

Auto-wires the [trail](https://github.com/Pratham-Mishra04/trail) MCP server into Claude Code and bundles a debugging skill that walks the agent through capturing → querying → cleanup of runtime instrumentation.

## What this gives you

- **Auto-registered MCP server** (`list_sessions`, `get_logs`) — no manual `claude mcp add` step
- **`/trail:debug-with-trail` skill** that triggers automatically when you ask about debugging, errors, crashes, or "what's happening at runtime", and follows a 4-phase workflow:
  1. Verify trail is installed and the right process is being captured
  2. Read existing logs first (cheapest answer)
  3. Add targeted log statements with a unique cleanup marker, restart, capture, query — only when needed
  4. **Mandatory cleanup** of every line added in step 3, verified by `grep`

## Prerequisites

The plugin only wires up Claude Code — you still need the `trail` binary on your `PATH`.

```bash
# macOS + Linux
brew install Pratham-Mishra04/trail/trail

# or
go install github.com/Pratham-Mishra04/trail@latest
```

Verify:

```bash
trail version
```

## Install

Add the marketplace, then install the plugin:

```bash
# In Claude Code:
/plugin marketplace add Pratham-Mishra04/trail
/plugin install trail@pratham
/reload-plugins
```

That's it. The MCP server starts on the next session; the debug skill is auto-discoverable.

## Verify it's working

Inside Claude Code, ask:

> *List my trail sessions.*

You should see Claude invoke the `list_sessions` tool. If you have no sessions yet, capture one first:

```bash
trail run -- <your command>
```

Then try again.

## How the debug skill triggers

The skill auto-invokes when you ask things like:

- "Why is my server returning 500s?"
- "Help me debug this — it crashes after a few minutes"
- "What's in the logs from my last run?"
- "Trace why the worker isn't picking up jobs"

The skill body is verbatim instructions to the agent; you can read it at [`skills/debug-with-trail/SKILL.md`](./skills/debug-with-trail/SKILL.md).

## Don't want the plugin?

You don't need it — the binary works on its own. Manual setup for Claude Code:

```bash
claude mcp add trail -s user -- trail mcp
```

For Cursor / Windsurf / Claude Desktop, see the config snippets in the [main trail README](https://github.com/Pratham-Mishra04/trail#editor-setup).

The trade-off: without the plugin, you don't get the debug skill — the agent has to figure out how to use the MCP tools on its own from their descriptions. Usable, just less guided.

## Updating

The plugin uses the `version` field in `plugin.json`. To update after a new release:

```bash
/plugin update trail@pratham
```

## Local development

If you've cloned the trail repo and want to test plugin changes locally:

```bash
# In Claude Code:
/plugin marketplace add /path/to/trail
/plugin install trail@pratham
/reload-plugins
```

Then edit files under `integrations/claude-code/` and `/reload-plugins` to pick up changes.
