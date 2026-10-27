# Flockdeck MCP server

`flockdeck mcp-server` is a stdio MCP server built into the `flockdeck` binary. A structured pane's agent starts it from the `mcpServers` entry named `flockdeck` that Flockdeck puts in `session/new` (see [ACP adapters](acp-adapters.md)). It is hidden from the picker and is not run by hand. It is the way agents message each other once the shell inbox is retired (see [ACP direction](acp-direction.md)).

An agent gets the entry when its catalog entry has the `mcp` flag and the workspace has a server to point it at. Structured panes are still opt-in, so no pane starts it until they are turned on.

## Tools

`send_message(to, text)` sends a message to another agent pane of the same project.

- `to` is a pane id, a pane name or a role, resolved as `flockdeck msg send` resolves them. A role reaches every pane running it except the sender.
- `text` is at most 8192 bytes.
- The sender is the pane the server was started for. The tool takes no sender argument, and an argument it does not list (`pane`, `from`) is refused.
- The message is recorded in the receiver's inbox, under the same limits as `flockdeck msg` (100 messages per inbox). The result lists where it went: `{"sent":[{"id":4,"pane":"...","to":"reviewer"}]}`, and `failed` names the panes of a role that did not get it. Delivery to the receiver as a prompt is T187, so today the receiver sees the message through the existing inbox paths.

`list_peers` lists the agent panes of the caller's project: `pane`, `label`, `role`, `agent`, `status`, and `self` on the caller's own entry. Panes of other projects, shells, models, directories and tokens are not shown.

A wrong argument type, a missing argument, an unknown argument or an empty `to` or `text` is a JSON-RPC error `-32602`. A refusal from Flockdeck (no such pane, inbox full, message too long, rate limit) comes back as a tool result with `isError` set, and its text says why.

## Identity

The entry's `env` has `FLOCKDECK_TOKEN` and `FLOCKDECK_API`. The token is 32 random bytes issued for one pane; Flockdeck keeps only its hash. The server sends it as `Authorization: Bearer` to the loopback API, and Flockdeck takes the sender from the token. Nothing the model writes can change it.

- A new session of the pane gets a new token and the old one stops working. Closing the pane revokes its token.
- The token works on `POST /mcp/send` and `POST /mcp/peers` only. The run's shared token is not accepted there and a pane token is not accepted by any other endpoint.
- The server refuses to start unless `FLOCKDECK_API` is an `http://` address on a loopback IP, so the token is not sent anywhere else.
- A request body with a field the endpoint does not define is refused with 400, and bodies over 49 KiB with 413.

## Limits

Per pane, as token buckets kept by Flockdeck, not by the server process:

| Call | Burst | Refill |
| --- | --- | --- |
| `send_message` | 10 | 1 every 3 seconds (20 a minute) |
| `list_peers` | 30 | 1 every second |

Every call with a valid token counts, a refused one too. Over the limit the answer is 429 with `Retry-After`. The server reads one message per line, at most 1 MiB, and answers a longer line with a parse error and skips it. It answers one request at a time.

## Recordings

The recorder replaces every `env` value of every server in a `session/new` (`internal/record`), so `FLOCKDECK_TOKEN` is not written to a recording. The server writes nothing to stderr and never logs a request, a token or a message.
