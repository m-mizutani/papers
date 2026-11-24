# Connect an agent

AI agents read and write papers through the MCP endpoint at `/mcp` or the REST API at `/api/v1`. They act for the person who issued their token, and their blocks are marked as written by the agent.

1. Open Settings and issue a token for the agent.
2. Add the MCP server to the agent's configuration.
3. Ask the agent to read this workspace's rules with `papers_guide`.

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
