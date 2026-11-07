# Flockdeck MCP server

`flockdeck mcp-server` is a stdio MCP server built into the `flockdeck` binary. A structured pane's agent starts it from the `mcpServers` entry named `flockdeck` that Flockdeck puts in `session/new` (see [ACP adapters](acp-adapters.md)). It is hidden from the picker and is not run by hand. It is how a structured pane's agent messages others and starts helpers. The shell inbox and `flockdeck msg` are the routes of terminal panes only (see [ACP direction](acp-direction.md)).

An agent gets the entry when its catalog entry has the `mcp` flag and the workspace has a server to point it at. Structured panes are still opt-in, so no pane starts it until they are turned on.

## Tools

`send_message(to, text)` sends a message to another agent pane of the same project.

- `to` is a pane id, a pane name or a role, resolved as `flockdeck msg send` resolves them. A role reaches every pane running it except the sender.
- `text` is at most 8192 bytes.
- The sender is the pane the server was started for. The tool takes no sender argument, and an argument it does not list (`pane`, `from`) is refused.
- The message is recorded in the receiver's inbox, under the same limits as `flockdeck msg` (100 messages per inbox). The result lists where it went: `{"sent":[{"id":4,"pane":"...","to":"reviewer"}]}`, and `failed` names the panes of a role that did not get it. Delivery to the receiver as a prompt is T187, so today the receiver sees the message through the existing inbox paths.

`list_peers` lists the agent panes of the caller's project: `pane`, `label`, `role`, `agent`, `status`, and `self` on the caller's own entry. Panes of other projects, shells, models, directories and tokens are not shown.

`spawn(task, ...)` starts a helper agent for the pane, as `flockdeck spawn` does for a terminal pane. It is the structured pane's way to hand work out, since a structured pane has no shell to run the command in.

| Argument | Type | Meaning |
| --- | --- | --- |
| `task` | string, required | What the helper is to do. |
| `role` | string | The role to start the helper as, one of the ids `flockdeck agents` lists. The role's agent and model apply where `agent` and `model` are not given. |
| `name` | string | What the helper works on, in 2 to 5 words. Cleaned and cut to 40 characters; made from the task when empty. |
| `tab` | string | The tab to put the helper in: the tab of that title in the caller's project, made if there is none. At most 60 characters, no control or format characters. Takes precedence over `split`. |
| `worktree` | string | The branch of a git worktree made for the helper. |
| `no_worktree` | boolean | Start the helper in the caller's checkout. Cannot go with `worktree`. |
| `split` | boolean | Place the helper beside the caller instead of in a new tab. |
| `agent`, `model` | string | Which agent and which of its models to ask for. |
| `terminal` | boolean | Ask for the terminal version of the agent. |

The tool builds the same request `flockdeck spawn` posts and gives it to the same handler in Flockdeck, so the rules are the CLI's: the caller's role says which roles it may start (and whether it may start an agent with no role), the project has a limit on helpers, a helper in a role that works on its own branch gets a worktree and a branch named from its task unless `no_worktree` or a role that shares the checkout says otherwise, and the helper starts in the caller's project and in the folder of the caller's pane (or of its worktree). A refusal says which rule it hit. The parent of the helper is the pane of the token and cannot be set.

The tool offers no `shell`, `record`, `baton` or `task` option. `-shell` has no use for an agent that cannot read a terminal. `-record` stays something the user turns on. `-baton` and `-task` need the baton approval window and the task board, which are left to the command line for now. Asking for any of them is refused as an unknown argument.

The result is the answer of the command as JSON: `{"paneId":"...","cwd":"...","role":"worker","branch":"..."}`, with `uncommitted` and `uncommittedUnknown` when the helper was given a default worktree and the caller's checkout had files the worktree does not. Keep the pane id; `list_peers` shows it too. The call returns when the pane has started, which can take up to a minute while a worktree is made. If it times out the helper may still be starting, so check `list_peers` before spawning again.

A wrong argument type, a missing argument, an unknown argument or an empty `to` or `text` is a JSON-RPC error `-32602`. A refusal from Flockdeck (no such pane, inbox full, message too long, rate limit) comes back as a tool result with `isError` set, and its text says why.

A structured pane whose role may start agents has a Starting agents section in its briefing that names the tool and its arguments.

## Identity

The entry's `env` has `FLOCKDECK_TOKEN` and `FLOCKDECK_API`. The token is 32 random bytes issued for one pane; Flockdeck keeps only its hash. The server sends it as `Authorization: Bearer` to the loopback API, and Flockdeck takes the sender from the token. Nothing the model writes can change it.

- A new session of the pane gets a new token and the old one stops working. Closing the pane revokes its token.
- The token works on `POST /mcp/send`, `POST /mcp/peers` and `POST /mcp/spawn` only. The run's shared token is not accepted there and a pane token is not accepted by any other endpoint.
- The server refuses to start unless `FLOCKDECK_API` is an `http://` address on a loopback IP, so the token is not sent anywhere else.
- A request body with a field the endpoint does not define is refused with 400, and bodies over 49 KiB with 413.

## Limits

Per pane, as token buckets kept by Flockdeck, not by the server process:

| Call | Burst | Refill |
| --- | --- | --- |
| `send_message` | 10 | 1 every 3 seconds (20 a minute) |
| `list_peers` | 30 | 1 every second |
| `spawn` | 3 | 1 every 10 seconds (6 a minute) |

Every call with a valid token counts, a refused one too. Over the limit the answer is 429 with `Retry-After`. The server reads one message per line, at most 1 MiB, and answers a longer line with a parse error and skips it. It answers one request at a time.

## Recordings

The recorder replaces every `env` value of every server in a `session/new` (`internal/record`), so `FLOCKDECK_TOKEN` is not written to a recording. The server writes nothing to stderr and never logs a request, a token or a message.
