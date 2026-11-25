# Connect an agent

AI agents read and write papers through the MCP endpoint at `/mcp` or the REST API at `/api/v1`. They act for the person who issued their token, and their blocks are marked as written by the agent.

1. Add the MCP server to the agent. Settings shows the command for Claude Code:

   ```sh
   claude mcp add --transport http papers http://localhost:8080/mcp
   ```

2. Start authenticating papers in the agent (in Claude Code, open `/mcp`). An approval screen opens in your browser; choose the access and press Allow. The agent receives its token directly, so there is nothing to copy, and the token appears in Settings, where you can revoke it.
3. Ask the agent to read this workspace's rules with `papers_guide`.

An agent without OAuth uses a token you issue in Settings, sent as a header:

```json
{
  "mcpServers": {
    "papers": {
      "type": "http",
      "url": "http://localhost:8080/mcp",
      "headers": { "Authorization": "Bearer pap_..." }
    }
  }
}
```

## Tools an agent uses most

1. `papers_read` returns a paper's blocks with their revisions.
2. `papers_write` sends a batch of operations based on those revisions.
3. `papers_save_version` keeps the current state as a version when the work is at a good point.
