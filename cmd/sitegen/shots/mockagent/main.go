// Command mockagent prints a scripted coding-agent session, for the staged
// screenshots on the site (see ../README.md). The scenario is picked from the
// name of the directory it runs in, so the worktree a pane is opened on decides
// what the pane shows. Nothing here talks to a model.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	reset   = "\x1b[0m"
	bold    = "\x1b[1m"
	gray    = "\x1b[90m"
	green   = "\x1b[32m"
	red     = "\x1b[31m"
	cyan    = "\x1b[36m"
	blue    = "\x1b[94m"
	magenta = "\x1b[95m"
	orange  = "\x1b[38;5;209m"
)

func out(s string)  { os.Stdout.WriteString(s) }
func line(s string) { out(s + reset + "\r\n") }
func pause(ms int)  { time.Sleep(time.Duration(ms) * time.Millisecond) }

func model() string {
	for i, a := range os.Args {
		if a == "--model" && i+1 < len(os.Args) {
			return os.Args[i+1]
		}
	}
	return ""
}

func main() {
	dir, _ := os.Getwd()
	name := filepath.Base(dir)
	switch {
	case strings.HasPrefix(name, "billing"):
		billingWaiting(name)
	case strings.HasPrefix(name, "design"):
		geminiDone(name)
	case strings.HasPrefix(name, "infra"):
		codexDone(name)
	case strings.HasSuffix(name, "-payments"):
		claudeWaiting(name)
	case strings.HasSuffix(name, "-checkout"):
		claudeWorking(name)
	case strings.HasSuffix(name, "-search"):
		codexWorking(name)
	default:
		geminiPlanner(name)
	}
	// Sit at the prompt. An empty select would be reported as a deadlock and
	// end the process, which the pane would show as exited.
	for {
		time.Sleep(time.Hour)
	}
}

// spin keeps a working pane working: a status line redrawn in place every
// second, with an occasional finished line printed above it.
func spin(label string, extra []string, codex bool) {
	start := time.Now()
	frames := []string{"✻", "✶", "✳", "✢", "·", "✢", "✳", "✶"}
	for i := 0; ; i++ {
		secs := int(time.Since(start).Seconds()) + 14
		if i > 0 && i%6 == 0 && len(extra) > 0 {
			out("\r\x1b[2K")
			line(extra[0])
			extra = extra[1:]
		}
		out("\r\x1b[2K")
		if codex {
			out(cyan + "• " + reset + bold + label + reset + gray + fmt.Sprintf(" (%ds • esc to interrupt)", secs) + reset)
		} else {
			out(orange + frames[i%len(frames)] + " " + label + "…" + reset + gray + fmt.Sprintf(" (%ds · esc to interrupt)", secs) + reset)
		}
		pause(900)
	}
}

func claudeHeader(dir string) {
	m := model()
	if m == "" {
		m = "default"
	}
	line(orange + "✻ " + reset + bold + "Claude Code" + reset + gray + "  " + m)
	line(gray + "  ~/code/" + dir)
	line("")
}

func tool(name, arg string, result ...string) {
	line(bold + "● " + name + reset + "(" + arg + ")")
	for i, r := range result {
		if i == 0 {
			line(gray + "  ⎿  " + reset + r)
		} else {
			line("     " + r)
		}
	}
}

func say(s string) { line("● " + s) }

// usage is what Claude Code hands its status line command after an answer:
// the session's tokens, its own cost estimate and, on a subscription, the
// five-hour and weekly windows. The mock hands it to the real bridge,
// `flockdeck statusline`, built beside it, the way Claude Code runs the
// command in a pane's settings file, so the figures reach the pane header by
// the same route and are read by the same code. The pane's own environment,
// set by the staged instance that started it, says where that instance
// listens and which pane this is. account names the Claude config folder the
// bridge keys the login by, a folder in the harness's work directory rather
// than the real one.
func usage(account, model string, in, out int64, cost float64, fiveHour, sevenDay float64) {
	api, token, pane := os.Getenv("FLOCKDECK_API"), os.Getenv("FLOCKDECK_TOKEN"), os.Getenv("FLOCKDECK_PANE")
	if api == "" || pane == "" {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	bin := filepath.Dir(self)
	work := filepath.Dir(bin)
	now := time.Now()
	limits := ""
	if fiveHour > 0 {
		limits = fmt.Sprintf(`,"rate_limits":{"five_hour":{"used_percentage":%g,"resets_at":%d},"seven_day":{"used_percentage":%g,"resets_at":%d}}`,
			fiveHour, now.Add(2*time.Hour+10*time.Minute).Unix(), sevenDay, now.Add(3*24*time.Hour+5*time.Hour).Unix())
	}
	input := fmt.Sprintf(`{"session_id":%q,"model":{"id":%q,"display_name":%q},"cost":{"total_cost_usd":%g},"context_window":{"total_input_tokens":%d,"total_output_tokens":%d}%s}`,
		pane, model, model, cost, in, out, limits)
	go func() {
		for {
			cmd := exec.Command(filepath.Join(bin, "flockdeck.exe"), "statusline", "--endpoint", api+"/usage", "--token", token, "--session", pane)
			cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+filepath.Join(work, "claude", account))
			cmd.Stdin = strings.NewReader(input)
			_ = cmd.Run()
			time.Sleep(20 * time.Second)
		}
	}()
}

func claudeWaiting(dir string) {
	claudeHeader(dir)
	usage("subscription", "claude-sonnet-5", 212_400, 9_300, 1.31, 72, 31)
	line(gray + "> " + reset + "Handle Stripe refund webhooks and mark")
	line(gray + "  " + reset + "the order as refunded")
	line("")
	pause(300)
	say("I'll look at how Stripe events are handled today.")
	tool("Search", "pattern: \"charge\\.\"", gray+"Found 3 files")
	tool("Update", "internal/payments/webhook.go",
		gray+"Updated webhook.go with 14 additions",
		green+"+ case \"charge.refunded\":",
		green+"+     return h.markRefunded(ctx, ev)")
	say("Orders need a refunded_at column first.")
	// Flockdeck ignores an agent's bell for its first five seconds, because
	// agents ring while starting up. The question comes after that, with a
	// bell, so the pane goes amber even if the pattern is not read.
	time.Sleep(6 * time.Second)
	permission("go run ./cmd/migrate up", "Apply migration 0042_order_refunds", "go run")
}

// permission asks to run a command the way Claude Code does: under a rule,
// the command and what it is for, then the numbered choices. It then keeps
// nudging, as a real agent does. A focus change in the window reaches the pane
// as input, which Flockdeck takes as the question being answered; the next
// bell puts the pane back to waiting. A bell draws nothing.
func permission(cmd, detail, again string) {
	out("\a")
	line(orange + strings.Repeat("─", 44))
	line(orange + bold + " Bash command")
	line("")
	line("   " + cmd)
	if detail != "" {
		line(gray + "   " + detail)
	}
	line("")
	line(" Do you want to proceed?")
	line(orange + " ❯ 1. Yes")
	line("   2. Yes, and don't ask again for " + again)
	line("   3. No, and tell Claude what to do")
	for {
		pause(1500)
		out("\a")
	}
}

func claudeWorking(dir string) {
	claudeHeader(dir)
	// On an API key: no windows, and Claude Code's own session cost.
	usage("apikey", "claude-opus-5", 318_000, 14_200, 2.14, 0, 0)
	line(gray + "> " + reset + "Move cart totals into a pricing package")
	line(gray + "  " + reset + "and keep the tests green")
	line("")
	pause(300)
	say("Totals are computed in three places. I'll pull them")
	line("  into one package and point the callers at it.")
	tool("Write", "internal/pricing/totals.go", gray+"Wrote 96 lines")
	tool("Update", "internal/checkout/cart.go",
		gray+"Updated cart.go with 4 additions and 38 removals",
		red+"- func (c *Cart) subtotal() int64 {",
		green+"+ totals := pricing.Totals(c.Lines, c.Discount)")
	line(bold + "● Update Todos")
	line(gray + "  ⎿  " + reset + green + "☒ " + reset + gray + "Extract pricing.Totals")
	line("     " + green + "☒ " + reset + gray + "Point cart and invoice at it")
	line("     " + "☐ Run the full test suite")
	line("")
	spin("Running tests", []string{
		gray + "  ok   shopfront/internal/pricing   0.41s",
		gray + "  ok   shopfront/internal/checkout  1.12s",
		gray + "  ok   shopfront/internal/invoice   0.37s",
	}, false)
}

func codexWorking(dir string) {
	m := model()
	if m == "" {
		m = "default"
	}
	line(bold + ">_ Codex" + reset + gray + "  " + m)
	line(gray + "   ~/code/" + dir)
	line("")
	line(cyan + "› " + reset + "Add GET /search with prefix matching over")
	line("  product names")
	line("")
	pause(300)
	line(cyan + "• " + reset + bold + "Explored")
	line(gray + "  └ Read internal/search/index.go")
	line(gray + "    Read internal/http/routes.go")
	line(cyan + "• " + reset + bold + "Edited " + reset + "internal/search/handler.go " + green + "+48 " + red + "-3")
	line(gray + "    21 " + green + "+func (h *Handler) Search(w http.ResponseWriter,")
	line(gray + "    22 " + green + "+    r *http.Request) {")
	line(gray + "    23 " + green + "+    q := strings.TrimSpace(r.URL.Query().Get(\"q\"))")
	line(cyan + "• " + reset + bold + "Ran " + reset + "go test ./internal/search/...")
	line(gray + "  └ ok  shopfront/internal/search  0.62s")
	line("")
	spin("Working", []string{
		cyan + "• " + reset + bold + "Edited " + reset + "internal/http/routes.go " + green + "+1",
		cyan + "• " + reset + bold + "Ran " + reset + "go vet ./...",
	}, true)
}

// billingWaiting is another project's agent stopped on a question, so that the
// rail has a project badged amber besides the one on screen.
func billingWaiting(dir string) {
	claudeHeader(dir)
	usage("subscription", "claude-sonnet-5", 64_800, 3_100, 0.42, 72, 31)
	line(gray + "> " + reset + "Retry failed invoice charges overnight")
	line("")
	pause(300)
	tool("Read", "internal/retry/policy.go", gray+"Read 88 lines")
	tool("Update", "internal/retry/policy.go", gray+"Updated policy.go with 22 additions")
	line("")
	time.Sleep(6 * time.Second)
	permission("go test ./internal/retry/...", "", "go test")
}

func geminiDone(dir string) {
	m := model()
	if m == "" {
		m = "default"
	}
	line(blue + bold + "✦ Gemini CLI" + reset + gray + "  " + m)
	line(gray + "  ~/code/" + dir)
	line("")
	line(gray + "> " + reset + "Check the button colours for contrast")
	line("")
	pause(300)
	line(magenta + "✦ " + reset + "All twelve pass AA; the ghost button now uses #5B6870.")
	line("")
	out(blue + "> " + reset + gray + "Type your message or @path/to/file" + reset)
}

func codexDone(dir string) {
	m := model()
	if m == "" {
		m = "default"
	}
	line(bold + ">_ Codex" + reset + gray + "  " + m)
	line(gray + "   ~/code/" + dir)
	line("")
	line(cyan + "› " + reset + "Pin the base images by digest")
	line("")
	pause(300)
	line(cyan + "• " + reset + bold + "Edited " + reset + "docker/Dockerfile " + green + "+3 " + red + "-3")
	line("")
	out(cyan + "› " + reset + gray + "Send a message" + reset)
}

func geminiPlanner(dir string) {
	m := model()
	if m == "" {
		m = "default"
	}
	line(blue + bold + "✦ Gemini CLI" + reset + gray + "  " + m)
	line(gray + "  ~/code/" + dir)
	line("")
	line(gray + "> " + reset + "Plan the work for the spring release")
	line("")
	pause(300)
	line(magenta + "✦ " + reset + "Four pieces, none of which touch each other:")
	line("")
	line("1. Move cart totals into a pricing package")
	line("2. Handle Stripe refund webhooks")
	line("3. Add a product search endpoint")
	line("4. Document the public API")
	line("")
	line(gray + "Each can go on its own branch and run in parallel.")
	line("")
	out(blue + "> " + reset + gray + "Type your message or @path/to/file" + reset)
}
