package webui

import (
	"regexp"
	"strings"
	"testing"
)

// Settings' sections, in the pop-over redesign's order (§5): eleven in four
// groups. Every control a section held keeps its id and is in the section it
// moved to, and in no other: Behaviour's defaults in General and its TypeSafe
// key and Jev switch in Status detection; Appearance's terminal half in
// Terminal; Agents' routing in Routing; General's version and updates in
// Account & plan.
func TestEverySettingIsInItsNewSection(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture({ update: { version: "9.9.9" } }));
h.click(h.$("btn-settings"));
const homes = {
  general: ["set-notifications", "set-auto-review-default", "set-fanout-sametab", "set-tips"],
  appearance: ["set-theme", "set-accent"],
  terminal: ["set-preview", "set-font-size", "set-font-family", "set-scrollback", "set-cursor", "set-cursor-blink", "set-screen-reader"],
  keybindings: ["set-keybind-reset-all", "set-keybind-closePane"],
  agents: ["set-agent-all", "set-agent-project", "set-statusline", "set-agent-routing", "set-agent-keys"],
  routing: ["set-route-all", "set-route-project", "set-route-floor", "set-route-strategy", "set-route-cross", "set-route-jev",
    "set-route-rules", "set-route-clear-log"],
  status: ["set-jev-key", "set-jev-status"],
  plan: ["set-updates", "set-check-update", "set-install", "set-versions", "set-sponsor", "set-legal", "set-plan-link"],
};
const every = Object.values(homes).flat();
for (const [section, ids] of Object.entries(homes)) {
  h.click(h.$("settings-tab-" + section));
  if (section === "agents" || section === "routing") h.recv(fixture({ agents: catalog({ routing: { every: { mode: "off" }, rules: [] } }), update: { version: "9.9.9" } }));
  const pane = h.$("settings-pane");
  for (const id of ids) assert.ok(h.$(id) && pane.contains(h.$(id)), id + " is not in " + section);
  for (const id of every.filter((x) => !ids.includes(x))) assert.ok(!h.$(id) || !pane.contains(h.$(id)), id + " is drawn in " + section + " as well");
}
assert.ok(!h.$("settings-tab-behaviour"), "Behaviour is still a section");
// The sections' own headings are sentence case, one name each.
h.click(h.$("settings-tab-keybindings"));
assert.strictEqual(h.$("settings-pane").querySelector(".set-title").textContent, "Keyboard");
h.click(h.$("settings-tab-agents"));
assert.strictEqual(h.$("settings-pane").querySelector(".set-title").textContent, "Agents & models");
// Terminal's preview is first, above the settings it shows.
h.click(h.$("settings-tab-terminal"));
const kids = h.$("settings-pane").children;
assert.ok(kids.indexOf(h.$("set-preview")) < kids.indexOf(h.$("set-font-size").closest(".set-row")), "the preview is not above the font");
// Where the key for Ask Jev is set is said by its new name.
h.click(h.$("settings-tab-routing"));
assert.ok(h.$("set-route-jev").closest(".set-row").textContent.includes("Settings › Status detection"),
  "Ask Jev still sends you to Behaviour for its key");
`)
}

// Find a setting keeps each setting's words with it: they find the section it
// is in now.
func TestFindASettingFindsTheNewHomes(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-settings"));
const find = h.$("settings-find");
const found = (q) => { find.value = q; find.oninput(); return h.$("settings-tabs").querySelectorAll("button").map((b) => b.id.slice("settings-tab-".length)); };
assert.deepStrictEqual(found("scrollback"), ["terminal"]);
assert.deepStrictEqual(found("cursor"), ["terminal"]);
assert.deepStrictEqual(found("theme"), ["appearance"]);
assert.deepStrictEqual(found("auto review"), ["general"]);
assert.deepStrictEqual(found("typesafe"), ["status"]);
assert.deepStrictEqual(found("floor"), ["routing"]);
assert.deepStrictEqual(found("version"), ["plan"]);
assert.deepStrictEqual(found("update"), ["plan"], "updates are still found in General");
assert.deepStrictEqual(found("keybindings"), ["keybindings"], "the old name no longer finds Keyboard");
assert.deepStrictEqual(found("relay"), ["remote", "plan"]);
`)
}

// Find a setting is in Settings' header, beside ? and ×, not in the list of
// sections; it goes with Settings when another surface takes the overlay.
func TestFindASettingIsInTheHeader(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-settings"));
const find = h.$("settings-find");
assert.ok(find.parentElement === h.$("overlay-head"), "Find a setting is not in the header");
const head = h.$("overlay-head").children;
assert.ok(head.indexOf(find) < head.indexOf(h.$("overlay-help")) && head.indexOf(h.$("overlay-help")) < head.indexOf(h.$("overlay-close")),
  "the header does not read Settings, Find, ?, ×");
assert.ok(!h.$("overlay-body").contains(find), "Find a setting is in the body");
h.click(h.$("overlay-help"));
await h.sleep(30);
assert.strictEqual(h.$("overlay-title").textContent, "Help");
assert.ok(!h.$("settings-find").isConnected, "Find a setting was left in Help's header");
h.key({ key: "Escape" });
assert.strictEqual(h.$("overlay-title").textContent, "Settings");
assert.ok(h.$("settings-find") && h.$("settings-find").parentElement === h.$("overlay-head"), "Settings came back without Find");
h.key({ key: "Escape" });
h.press("worktrees");
assert.ok(!h.$("settings-find").isConnected, "Find a setting was left in Worktrees' header");
`)
}

// Under 640px the list and a section are two pages: opening Settings shows
// the list; picking a section shows it alone, with "← All settings" at its
// top, which has the keyboard; that goes back to the list, on the section's
// tab. Typing in Find shows the list, where what it found is. Settings opened
// on a named section opens on that section.
func TestANarrowWindowShowsTheSettingsInTwoLevels(t *testing.T) {
	runFrontEnd(t, `
h.win.matchMedia = (q) => ({ matches: /max-width: 640px/.test(q), addEventListener() {} });
h.hello();
h.recv(fixture());
h.click(h.$("btn-settings"));
const wrap = h.$("settings");
assert.ok(!wrap.classList.contains("at-page"), "a narrow window does not open on the list");
assert.ok(h.doc.activeElement === h.$("settings-tab-general"));
h.key({ key: "ArrowDown" });
assert.ok(!wrap.classList.contains("at-page"), "walking the list with the arrows left it");
h.click(h.$("settings-tab-appearance"));
assert.ok(wrap.classList.contains("at-page"), "picking a section did not show it");
assert.ok(h.doc.activeElement === h.$("settings-back"), "the keyboard was left on the list that has gone");
assert.strictEqual(h.$("settings-back").textContent, "← All settings");
assert.ok(h.$("settings-pane").contains(h.$("set-theme")));
h.click(h.$("settings-back"));
assert.ok(!wrap.classList.contains("at-page"), "← All settings did not go back to the list");
assert.ok(h.doc.activeElement === h.$("settings-tab-appearance"), "the keyboard is not on the section it came from");
h.click(h.$("settings-tab-plan"));
const find = h.$("settings-find");
find.value = "cursor";
find.oninput();
assert.ok(!wrap.classList.contains("at-page"), "Find did not show the list of what it found");
find.value = "";
find.oninput();
h.key({ key: "Escape" });

// GitHub's own way into its settings opens on them.
h.press("settings");
h.key({ key: "Escape" });
h.click(h.$("btn-github"));
h.recv({ type: "ghStatus", cwd: "", installed: false, installManager: "winget", installUrl: "https://cli.github.com" });
const go = h.$("overlay-body").querySelectorAll("button").find((b) => b.textContent === "Open GitHub settings");
assert.ok(go, "GitHub offers no way to its settings");
h.click(go);
assert.ok(h.$("settings").classList.contains("at-page"), "a section asked for by name opened on the list");
assert.strictEqual(h.$("settings-tab-github").getAttribute("aria-selected"), "true");
assert.ok(h.doc.activeElement === h.$("settings-back"), "the keyboard is on the list that is not shown");
`)
}

// Remote access says in the list that it is on, where it is, drawn from an
// attribute so the tab's text stays its name.
func TestRemoteAccessIsMarkedOnInTheList(t *testing.T) {
	runFrontEnd(t, `
h.hello();
h.recv(fixture());
h.click(h.$("btn-settings"));
assert.ok(!h.$("settings-tab-remote").hasAttribute("data-tag"), "remote access off is marked on");
h.key({ key: "Escape" });
h.recv(fixture({ remote: { state: "connected", relay: "https://relay.example", hostId: "h1", viewers: 0, since: "2030-01-01T00:00:00Z" } }));
h.click(h.$("btn-settings"));
assert.strictEqual(h.$("settings-tab-remote").getAttribute("data-tag"), "on");
assert.strictEqual(h.$("settings-tab-remote").textContent, "Flockdeck Remote");
`)
}

// The group headings, the "on" tag and the two levels are drawn by app.css:
// the headings are not a tab's size or look, the section heads inside a
// section are sentence case, and the two levels hide the page not shown.
func TestTheSettingsLayoutIsInTheStylesheet(t *testing.T) {
	css := stripComments(readAsset(t, "app.css"))
	for _, sel := range []string{".settings-group", `.settings-tab[data-tag]::after`, ".settings-back", "#overlay-head > .settings-find"} {
		if ruleBody(css, sel) == "" {
			t.Errorf("app.css has no rule for %s", sel)
		}
	}
	if b := ruleBody(css, `.settings-tab[data-tag]::after`); !strings.Contains(b, "attr(data-tag)") {
		t.Errorf("the tag is not drawn from data-tag: %q", b)
	}
	// The last .set-sub rule wins: sentence case.
	subs := regexp.MustCompile(`(?m)^\.set-sub\s*\{([^}]*)\}`).FindAllStringSubmatch(css, -1)
	if len(subs) == 0 || !strings.Contains(subs[len(subs)-1][1], "text-transform: none") {
		t.Errorf("a section's heads are still upper case")
	}
	for _, want := range []string{
		`.settings:not(.at-page) > .settings-pane { display: none; }`,
		`.settings.at-page > .settings-nav { display: none; }`,
	} {
		if !strings.Contains(css, want) {
			t.Errorf("app.css does not have %s", want)
		}
	}
	// Settings takes the large dialog's size, not a size of its own.
	if b := ruleBody(css, "#overlay-panel.settings-panel"); strings.Contains(b, "width") {
		t.Errorf("Settings still has a width of its own: %q", b)
	}
}
