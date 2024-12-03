# mcp

Server implementation of the Model Context Protocol (MCP). Currently allows
tools to be exposed to LLMs supporting the protocol.

## Example

The example directory contains an example of exposing a tool to Claude to
compute a SHA-256 sum.

```
$ cd mcp/example
$ GOOS=darwin GOARCH=arm64 go build
```

Add the example server to your Claude config:

```
$ vim ~/Library/Application\ Support/Claude/claude_desktop_config.json
```

```json
{
  "mcpServers": {
    "sha256": {
      "command": "/path/to/example/binary"
    }
  }
}
```

We can then ask Claude:

> What is the sha256sum of "the rain in spain falls mainly on the plains"?

Claude will then perform a tool call against the server and return the
following response:

> The SHA-256 hash of "the rain in spain falls mainly on the plains" is:
> b65aacbdd951ff4cd8acef585d482ca4baef81fa0e32132b842fddca3b5590e9
