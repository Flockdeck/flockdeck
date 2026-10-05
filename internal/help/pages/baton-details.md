# Baton details

This page holds the detail behind [Handing work to another agent](#baton): what the
scrubber takes and leaves, when it looks again, how an agent's company is decided, and
how stored batons are kept and removed.

## What the scrubber takes

It takes values from the pane's environment and the checkout's `.env` files, the shapes
of common keys and tokens, long random-looking strings, and what is typed on a command
line or in a config file: `Authorization` credentials, database passwords after `-p`,
`htpasswd -b`, `sshpass`, `curl -u`, `export NAME value` and `setx name value`, cookie
values, npm tokens, JSON and Go literals and YAML blocks under a secret-looking name, and
`name: value` lines. It also takes `vault login TOKEN`,
`az login -p`, `doctl -t`, `unzip -P` and `zip -P`, `gpg --passphrase`, `7z -pPASSWORD`,
the value in `aws configure set aws_secret_access_key`, the password of `http -a`
(httpie), and the secret that `echo` or `printf` pipes into `docker login
--password-stdin` (or is given as `<<<`). A name that ends in `pass`, `passwd`, `pwd`,
`pw` or `key` (`db_pass`, `DBPW`, `signing_key`, and a bare `pw` or `key`) takes a value
that looks chosen: letters of both cases, a digit, or letters with a symbol in them
(`ABCD.EFGH.IJKL`), 8 characters or more, and 12 or more under `key`. Nothing under these
names is left alone for looking like code: `Hunter2(2024)Abc`, `{Winter2024!}`,
`Winter.Spring.Summer` and `Qwerty.AsdfGhjk` are taken. What is left alone is a call that
reads an environment variable by name and has nothing after it (`os.getenv("X")`,
`env.get("X")`), a call or slice of plain names (`bytes.Clone(key)`,
`append(k, other[:16]...)`, `keyMaterial[:n]`), and a comment after either that has no
word in it that looks chosen. `public_key`, `cache_key`, `primary_key`, `sort_key` and a
few like them are never taken. Invisible and full-width characters do not hide a secret.
Paths and branch names are kept.

When in doubt it redacts. A value under a secret's name is left only when it is plainly
not one:

- `none`, `null`, `true`, `false`, `required` and type names.
- Placeholders: `$TOKEN`, `${TOKEN}`, `<token>`, `{{ .Values.x }}`, `your-token-here`.
- A mask of eight or more of one of `*`, `•` or `#`. An `x` is not a mask:
  `password=xxxxxxxx` is redacted, and so is `password=****`.
- A call that reads the environment, when it is the whole value and holds only a name:
  `os.Getenv("X")`, `ENV['X']`, `process.env.X`, `<%= ENV['X'] %>`. A call with more
  after it (`os.Getenv("X") + "hunter2"`, `ENV.fetch("X", "default")`, or an ERB tag
  that holds a literal) is redacted to the end of the line, because the rest may be the
  secret.
- Dates under `*_at`, `*_expires`, `*_date` and `*_time` names, and a time as a number
  of 9 to 13 digits. Under a name that has a secret word in it (pass, password, secret,
  token, key, auth, credential) the number is kept only by a table in the code: the
  secret word has to be `token` or a `key_id`, the name has to say what the time is of
  (`expires`, `expiry`, `issued`, `created`, `updated`), and the number has to be 10 digits
  starting with 1 (seconds) or 13 starting with 1 (milliseconds). So
  `token_expires_at=1700000000` is kept, and `token_at=1712345678`,
  `password_expires=1712345678`, `api_key_expires=4412345678` and `password_at=987654321`
  are redacted. A number of fewer than 10 digits under a name with `expires` in it is
  taken for an amount (`token_expires=3600`).
- Numbers under names that say an amount (`max_tokens`, `TOKEN_BUCKET_SIZE`), and counts
  of model tokens (`input_tokens`), including several on one line
  (`input_tokens=1200 output_tokens=340`).
- A place, when the name says it is one (`*_file`, `*_path`, `*_dir`, `*_url`,
  `ssh_key`) and the value starts like one, or a file under `/run/secrets`. A path to a
  key file is still replaced, by its file name (`id_rsa`, `*.pem`, `*.key`), whatever the
  name says: `ssh_key: ~/.ssh/id_rsa` becomes `ssh_key: [REDACTED: key-file-path]`.
  A random value under `ssh_key` is a secret.
- A variable handed on with its key's name (`Token: token,`).

`password`, `changeme` and `admin` as a value are redacted, and so is anything with a
slash, colon or dot in it under a name that does not say it is a place. A URL's password
goes and its scheme, user and host stay, whatever the user is called
(`x-access-token`).

## Hidden characters

Every baton is cleaned of terminal escape sequences and control characters. It is also
cleaned of characters that draw nothing and can carry text a person does not see: the
Unicode tag characters (U+E0000 to U+E007F) and the variation selector supplement
(U+E0100 to U+E01EF), zero width spaces, bidi controls, the word joiner and the
invisible operators, soft hyphens, and the Hangul and Khmer fillers. A zero width joiner
or non-joiner, or a variation selector, next to an ASCII character is removed too. Between
non-ASCII characters they stay, because emoji, Persian and Hindi are written with them.
A run of them is cut to two, so a few hidden bits can still ride between non-ASCII characters.
The `<baton>` fence is escaped in its ASCII form and in forms that look like it: with
no-break or ideographic spaces inside, or in full-width letters.

## What it misses or takes wrongly

- Bare 32 and 40 character hex strings, UUIDs, and hex of 41 to 63 characters are kept,
  because commit ids and checksums fill a baton. A provider key that looks like that, or
  a hex session id in a `Cookie`, is not caught without a known prefix or a name like
  `api_key`. Hex of 64 characters or more is removed.
- Secrets that are short, ordinary words, or not named as one, and secrets in a request
  body or literal under a name it does not know.
- `mysql -p word` is read as a database name unless the word looks like a password (eight
  characters or more, with a digit or both cases); `-pPASSWORD` is always taken. `7z x -p
  word` is the same with an archive name, and is not taken; `7z a -pPASSWORD` is.
- `NAME=value&other=1` on its own line: the value runs to the end of the line, so
  `&other=1` goes with it. In a URL's query only the value goes.
- `export name value` with a lower case name is taken only when the value is one word that
  looks chosen (8 characters or more with a digit or both cases); `setx name value` is
  taken whatever the value is.
- A passphrase with `(`, `*` or `//` in it in a `name: value` line, which looks like code.
- Shell syntax past quotes, `$'...'` and line continuations, such as a heredoc body.
- `passwd value` and `user passphrase` in prose, with no `=` or `:`, are not taken.
- A key wrapped over lines is marked on every line of a run of one width. A run of three
  lines or more is taken when one line has a provider's prefix or looks like a key by
  itself, or the joined run is 40 characters or more with several kinds of characters. A
  run of two lines is taken when the first is at least 16 characters and the second at
  least 6 with a digit or both cases, and the two look random, and can still keep a line (about one in a hundred
  in the tests). Columns of hex ids of one length, UUIDs and names with a hex tail
  (`user-3f9a...`) are lists, and are not read as a wrapped key.
- A short value under a name that ends in `key` (under 12 characters, `ssh_key=a.b.c`,
  `key = x[0]x`) is not taken: names like that hold identifiers and indexes in code too
  often. Under `pass`, `pwd` and `pw` names the least is 8.
- Some lines of source code that mention `key`, `pass`, `pw` or `pwd` are marked though
  they are code. Counted against every line, per line, the scrubber changes 0.3% (405 of
  163,613) of the non-test Go source of net and crypto in the Go 1.27 standard library and
  0.9% (134 of 15,243) of this repository's `internal/webui/assets/app.js`; some of those are
  other things it takes, such as long random-looking identifiers. The larger figures that
  follow are a different measure: the share of lines that contain the letters `key`, `pass`, `pw`
  or `pwd` anywhere in the line, in any case (a substring match, so `monkey`, `passport` and
  `power` count) that Flockdeck changes: 2.6% (157 of 5958) of the lines of the non-test Go
  source of net and crypto in the Go 1.27 standard library, and 14.2% (119 of 839) of this
  repository's `internal/webui/assets/app.js`. Counting only lines where one of the four is a
  word of its own (not next to another letter) it is 5.6% (110 of 1954) and 34.9% (117 of
  335). That is deliberate: a password with brackets
  or dots in it looks like code, and under the names `key`, `pass`, `pw` and `pwd` nothing is
  left alone for that. What is kept is an environment read by name, a call or a slice of
  plain names, and a value too short to be chosen.
- Text taken that is not a secret: random-looking identifiers and base64 in paths,
  `const tokenKey = "auth-token"`, `password_field_label = "Password"`, and a literal
  `[REDACTED: x]` of a kind Flockdeck does not make, which becomes a `secret-value` mark.

## When it looks again

Scrubbing again, up to four passes, happens when the first pass changed something and a
mark has something but white space next to it, a command that reads its words by position
(`htpasswd`, `curl`, `mysql`, `sshpass`, `docker`) or a key file name is in the text, or
a marked line names a secret. That is for a text up to 64 KB. Above that only a mark with
a word or an odd space stuck to it, or `htpasswd` or a key file name, gets another look,
so a very large text costs one pass and a second look is not promised. A word with a
digit next to a no-break space can be taken with a token.

## Deciding an agent's company

The company of the agent a helper will run decides whether a baton needs approval. A CLI
counts as unknown when a setting that applies to it sends it elsewhere:

- Claude Code: `ANTHROPIC_BASE_URL`, `ANTHROPIC_BEDROCK_BASE_URL`,
  `ANTHROPIC_VERTEX_BASE_URL`, `ANTHROPIC_FOUNDRY_BASE_URL`, `ANTHROPIC_FOUNDRY_RESOURCE`,
  `CLAUDE_CODE_USE_BEDROCK`, `CLAUDE_CODE_USE_VERTEX` and `CLAUDE_CODE_USE_FOUNDRY`.
- Codex: `OPENAI_BASE_URL`, `OPENAI_API_BASE`, `AZURE_OPENAI_ENDPOINT`, and `config.toml`
  (in `$CODEX_HOME` when that is set, else `~/.codex`). The file is parsed as TOML (tables,
  arrays of tables, dotted and quoted keys, all four kinds of string, arrays over several
  lines, inline tables, comments, CRLF, a byte order mark). The settings that count are the
  top-level ones with the profile that `profile` selects on top. A `model_provider` that is
  not `openai`, any `oss_provider`, an `openai_base_url` whose host is not api.openai.com,
  and the `base_url` of the active provider's table (`[model_providers.<id>]`) whose host is
  not OpenAI's make the company another one. A `base_url` in any other table, an MCP
  server's for one, and text inside a multi-line string, do not count. A file that cannot
  be parsed, is over 1 MB, or is there and cannot be read, makes the company unknown.
  What a command line gives Codex (`-c`, `--profile`, `--oss`) is not read, and neither
  are `chatgpt_base_url`, a `.codex/config.toml` in a project, `~/.codex/.env` and a
  system-wide Codex config. How Codex itself reads its config was not checked against
  Codex.
- Gemini: `GOOGLE_GEMINI_BASE_URL`, `GEMINI_API_BASE_URL`, `GOOGLE_VERTEX_BASE_URL` and
  `GOOGLE_GENAI_USE_VERTEXAI`.

They are read from the agent's own env block, Flockdeck's environment and, for Claude
Code, the `env` of the user's `settings.json`, of the managed settings file, and of
`settings.json` and `settings.local.json` in the project folder and its parents up to the
git root (only the folder itself when it is in no git repository). The managed file is
`C:\Program Files\ClaudeCode\managed-settings.json` on Windows, `/Library/Application
Support/ClaudeCode/managed-settings.json` on macOS and `/etc/claude-code/managed-settings.json`
on Linux; those paths are from Claude Code's documentation and were not checked on a
managed machine. The `.claude/settings.json` and `.claude/settings.local.json` that
the worktree will hold are read from the object store: the branch's own when the branch
exists, and for a new branch the HEAD of the checkout it is cut from. Only what a commit
contains is in a worktree, so a `settings.local.json` is there only if the repository
committed it. Names are matched in any case (`.Claude/Settings.json`), every spelling that
is found is read, and a `.claude` committed as a link to a folder of the same commit is
followed through folders of the commit only. The target is read exactly as written: one with
a `.` or `..` part (so `./cfg` too), an empty part, a space at either end, a control
character, a backslash, a colon, or an absolute path, one that goes through another link, or
that is not a folder of the commit, makes the company unknown; so does a `.claude` that is a
submodule. The checkout's working copy, with its own `settings.local.json`, is read
as well, and if either says another company, or cannot be read, it asks. A link, a pipe or a device where a settings file should be is
refused without being opened; a link to a regular file is followed. A file with nothing in
it, or only white space, has no settings; a file in UTF-16 is decoded; an `env` that is not
an object sets nothing; names in `managed-settings.d` that start with a dot or end in `~`,
`.swp` or `.tmp` are not read. Numbers and booleans count. A byte order mark, comments and trailing commas are
allowed. This fails closed: a settings file that is there and cannot be opened (a
permission refused, a folder in its place), is over 1 MB (it is not read), or cannot be
read as JSON makes the company unknown, and the notice names the file by its whole path,
so that the user's, the project's and the managed one can be told apart. A settings file
committed in the branch that git cannot give, that is over the limit, or that is a link in the
commit, does the same. The `env` key is read by its exact spelling: `ENV` or `Env` beside it,
or `env` twice, makes the file unreadable, and variable names are matched in any case. A file that is not
there, or a `.claude` that is a file, has no settings. The folder `managed-settings.d`
beside the managed file is read for its `.json` files and fails the same way if it cannot
be listed; that this folder exists is from Claude Code's documentation and was not
checked on a managed machine. Settings a macOS configuration profile (MDM) delivers are
not read. A planned worktree that is not made yet is read as itself and the checkout it is
cut from, the same as when it has been made.

Empty, `0`, `false` and the vendor's own host (a trailing dot too) are not overrides, and
one vendor's setting does not touch another's CLI. An agent is taken for a vendor by its
program or, for a wrapper, its id. An empty entry in an agent's own env does not hide the
same setting in the environment. The notice says why a company is not known ("through a
gateway or proxy (host)"), with the host only.

The company is worked out again where the helper really runs, once its worktree exists,
because the files there can differ from what was read to decide. If you were asked, it
has to be the company you saw. If nobody was asked, it has to be the company the baton
came from: settings that the new folder holds, or that a branch commits, which would send
the helper elsewhere stop it, and nothing starts. The same agent going to a different
company only because of where it works counts as a change and asks. A worktree and branch
that this request made are removed when the start fails, but only if the worktree holds
nothing beyond the files git checked out and no file git is told not to look at
(any `--assume-unchanged` file, and any `--skip-worktree` file unless the repository uses a
sparse checkout): no changed or untracked file, no ignored file
(`.env`, `node_modules`, an IDE folder, build output, anything a hook wrote), no empty
folder. git status leaves ignored files out and a plain removal deletes them, so Flockdeck
also walks the folder against `git ls-files`, and keeps the worktree if anything is extra,
naming up to five. An empty folder for a submodule does not count. The branch is deleted
only if this request made it and it is still at the commit it was made at, and git is told
that commit when it deletes, so a branch that moves in between is left. A branch that
existed before is never deleted.

## The approval record

A damaged `sources.json` is kept as `sources.json.bad` (a damaged overflow record as
`overflow.json.bad`); one that cannot be read is left alone and the failure goes to
`error.log`. The notice goes only to a connection that opened with an Origin, each window
gets its own one-use token, and an endpoint is only ever shown, and sent to the window, as
its host. A window that is the only one open and is remote-only cannot approve. The
command waits 45 seconds for the approval as well as its usual minute. Linux refuses NFS,
SMB, CIFS, AFS, Ceph, Coda and 9P for a notes file; FUSE and virtiofs are not checked.

## Keeping and removing batons

What a baton is built from is cut to its own maximum before it is scrubbed, a text over 1 MB
has its middle clipped, and the whole build has 5 seconds: what is not scrubbed by then is
replaced by `[REDACTED: too-large-to-scrub]` and never passed on. A hostile or very large
conversation is therefore bounded, and loses text, not secrets, for what a build reads. A baton that is
stored or edited is scrubbed again when it is saved or sent, up to 1 MB as one text (so a key
wrapped over lines is not cut in two); over 1 MB the middle is replaced by a `[clipped]`
line. Scrubbing 1 MB of text that is made to be slow takes from 1 to 9 seconds, and the
5-second budget is looked at between texts, not inside one. Plainly, three limits of this:
the header of a baton (id, title, folder, branch, pane, agent, model) is cut to 4 KB before
it is scrubbed, so a secret that starts in the last few bytes of such a field keeps up to
about six characters of it, which is below what any pattern recognises, and a value that is
legitimately longer is cut; scrubbing a text of up to 1 MB as one is more than linear (worst
cases measured: 1.4 s for 256 KB, 4.0 s for 512 KB, 9.8 s for 1 MB) and cannot be cancelled
once a text has begun, so it runs off the workspace goroutine, in goroutines that hold no
global lock; and a stored baton is scrubbed twice when it is started from (once when it is
read and once on the text that is sent).

Batons are kept in Flockdeck's state directory, on this machine. When the next baton is
saved, batons not made, shown or used for 30 days are removed, with their overflow files:
one at a time when there are fewer than four, else at most half, the oldest first. A gap
of weeks between saves is not a fault, so a machine that saves rarely still prunes.

Nothing is removed when:

- the clock is earlier than at the last run, or was set while Flockdeck ran (one prune is
  skipped after a long sleep too, since the two clocks then disagree);
- a baton is dated in the future;
- the newest file in the state folder is more than a year behind;
- the state folder cannot be listed (said in the log once a day);
- a saved layout cannot be read, for up to 30 days from the first time it was seen
  unreadable. After that pruning goes on. Each unreadable file is tried by another route
  (shared access on Windows), and if that reads it, what it names is protected. If it
  cannot be read that way either, every baton that only that layout names becomes
  prunable: half of the batons a run, the oldest first.

A baton that a layout names is never counted as old and never removed, however old it
looks. A baton that no layout names and that looks more than five years old is never
removed either (that is the safe side), whatever the clock says; it is skipped one by one
and said in the log once a day. When half of them or more look that old the run is not
written down, since the clock is the likelier fault. Runs that stop for the clock write
nothing down, and a recorded time that is a day ahead is ignored. The record of what
pruning has seen (`prune.json`) keeps its other entries when one is written; one that is
empty or damaged is kept as `prune.json.bad`, started again with the current time, and
pruning goes on from the next run, except when the newest file in the state folder is
more than a year old, which stops the run as a wrong clock does. A record that exists and
cannot be read at all (a folder in its place, a permission refused) is not damage and is
not moved: pruning stops while it is so, said once a day in the log, and after 30 days of it
pruning goes on with that record taken as empty. A baton used while it was unreadable is
not forgotten: a use is also written on the baton's own file time, which the age rule reads
first, so it is kept for 30 days from that use. A baton younger than the retention window,
named by a saved layout or in use is not removed either. Each record's own time of being
unreadable is forgotten as soon as that record reads fine, whatever the others do. Any other record that is
there and cannot be read (`used.json`, `overflow.json`, `sources.json`, the retries
record) is kept aside as `<name>.bad` (`.bad.1` up to `.bad.99`; none is written over, and
with all 100 taken no more are kept, which is said once)
and started again empty, and nothing is pruned in that run or for 24 hours after, since
batons used a moment ago may look unused. A record that is empty or cut short is read from
the newest complete copy a write left beside it, and that copy is not swept while it is
the only one. A baton stays if any saved layout names it, in any project, or a pane was
started from it in the last hour.

Overflow files are removed afterwards in the background, one cleanup at a time, and given
up on after five failed tries. A record is renamed into place; where Windows refuses that
because another process has the file open, it is tried five times and then written in
place, up to three tries; if they fail the record is put back as it was, and the whole new
copy stays beside it if even that fails. A record that cannot be read or parsed is waited
for 50 ms before it is read, with no lock held, before it is called damaged, since another
process may be writing it in place. The mark that says a pane was started from a
baton is made again once if its folder was removed between the two steps, and a failure
to make it is logged once for each state folder. Two Flockdeck processes writing a record at the same instant
keep the later write.
Stale temporary files are swept after an hour.
