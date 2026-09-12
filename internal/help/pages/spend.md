# Spend and limits

A pane's header says what its agent has spent in the conversation, and how close it is to the usage limit it runs under, so the agent that is burning money, or about to stop, can be picked out of a tab of six without asking each one.

## What the header shows

Two small figures sit after the tool name, before the processor and memory figure:

- **What it has spent.** `~$1.24` is an estimate in US dollars. `84k tok` is tokens read and written, shown where there is no price to put on them, such as a local model. A `+` after the money, as in `~$0.40+`, means some of the tokens had no price, so the figure is a floor.
- **The tightest limit.** `5h 72%` means 72% of the five-hour window is used; `7d` is the weekly one. A bar under it fills as the window does. It turns amber at 80% and red at 95%.

Hover over either one for the rest: every window and when it resets, the tokens in, cached and out, and where the money figure came from.

Nothing is shown for an agent that reports nothing. Today that is every agent except Claude Code and the built-in API agents.

## Every money figure is an estimate

No figure here is your bill, and each one is written with `~` to say so.

- **The built-in API agents** count the tokens of every call exactly, and Flockdeck prices them from a table of published prices compiled into the app, for Anthropic's, OpenAI's and Google's models. It is the same table the picker shows prices from. The tooltip says the date the prices were checked. A model is priced only when its id is one the table names, or a dated snapshot of one; any other is shown in tokens only, because a made-up price is worse than none.
- **Claude Code** works out its own session cost at list prices, and the header shows that figure, marked as Claude Code's own. It can differ from what you are charged: for example, a data-residency premium or a negotiated rate is not in it.

Tokens are exact and carry no `~`.

## On a Claude subscription

If you use Claude Code on a Pro or Max plan, your money is fixed and what stops you is the usage window. So the header leads with the window, and shows tokens rather than dollars. The dollar figure goes in the tooltip, as what those tokens would have cost on the API.

A limit belongs to your login, not to one pane. Every Claude pane on the same login shares the same five-hour and weekly windows, and so does claude.ai, and Claude Code on another machine. A reading from any pane is shown in all of them. Use elsewhere moves the window between readings, so a reading more than ten minutes old says when it was taken.

## How Claude Code's limits are read

Claude Code hands its limits to one place only: the command that draws its status line, the line under its prompt. To read them, Flockdeck puts a small command of its own in that place for its panes. That command passes the figures to Flockdeck, then runs your own status line command with exactly the same input, and prints what yours prints. Your status line looks as it did.

Where you have no status line of your own, doing this would cost you something: Claude Code hides most of the keyboard hints in its footer whenever any status line is set. So by default Flockdeck only does it where you already have a status line, and there it changes nothing you can see.

To choose, open **Settings › Agents › Claude Code's usage limits**:

- **Only where I have a status line** is the default.
- **Always** shows the limits in every Claude pane. Where you have no status line of your own, the line under Claude's prompt is left empty and the footer hints go.
- **Never** leaves Claude Code's status line alone. The header then shows nothing for Claude panes.

The choice applies to a pane when it starts. Use **Restart pane** from the command palette for one that is already running. Your own status line is looked up the way Claude Code looks for it: in the project's `.claude/settings.local.json`, then `.claude/settings.json`, then your own `settings.json` in Claude Code's folder. If you change it, restart the pane so the new command is used.

The windows only appear for Pro and Max subscribers, and only after the first answer in a session. On an API key, a Claude pane shows its session cost instead.

## What stays on your machine

Everything here is worked out on this computer, from what the agents themselves report. Flockdeck sends nothing about your spending anywhere, and makes no network request to find it out: no usage API, no price feed. If remote access is on, the figures travel with the rest of your panes' state to your own devices, through the relay, as the pane headers do.

The figures are kept in memory only for now. They start again when a conversation does, with `/clear` or a new session, and when Flockdeck restarts.
