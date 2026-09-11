# Third-party notices

Flockdeck is distributed as a single binary with the front end compiled into
it, so everything listed here travels inside every release. Each licence below
requires its copyright notice to be reproduced wherever the software is
redistributed, and this file is how that is done.

Every dependency is permissive — MIT, ISC or BSD 3-Clause. There is no copyleft
anywhere in the tree, so a binary built from this repository can be
redistributed under the terms in [LICENSE](LICENSE) together with the notices
below.

---

## Front end, compiled into the binary

The files in `internal/webui/assets/vendor/` are builds of the `@xterm/xterm`,
`@xterm/addon-fit`, `@xterm/addon-search` and `@xterm/addon-webgl` packages,
vendored rather than fetched at build time. They are minified, and the minifier
strips the banner comment the licence asks for, so it is restored at the top of
each file and reproduced here.

### xterm.js

Copyright (c) 2017-2019, The xterm.js authors (https://github.com/xtermjs/xterm.js)
Copyright (c) 2014-2016, SourceLair, Private Company (https://www.sourcelair.com)
Copyright (c) 2012-2013, Christopher Jeffrey (https://github.com/chjj/)

```
MIT License

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

---

## Go modules, linked into the binary

These are the modules the binary actually links, as reported by
`go version -m flockdeck`. Test-only and build-only dependencies are not
distributed and are not listed.

### github.com/aymanbagabas/go-pty — MIT

Copyright (c) 2023 Ayman Bagabas

### github.com/yuin/goldmark — MIT

Copyright (c) 2019 Yusuke Inuzuka

### github.com/xtaci/smux — MIT

Copyright (c) 2016-2017 xtaci

All three are under the MIT licence, whose full text is reproduced above and in
[LICENSE](LICENSE).

### github.com/coder/websocket — ISC

Copyright (c) 2025 Coder

```
Permission to use, copy, modify, and distribute this software for any
purpose with or without fee is hereby granted, provided that the above
copyright notice and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
```

### github.com/google/uuid — BSD 3-Clause

Copyright (c) 2009, 2014 Google Inc. All rights reserved.

### golang.org/x/crypto, golang.org/x/sys, rsc.io/qr — BSD 3-Clause

Copyright (c) 2009 The Go Authors. All rights reserved.

`rsc.io/qr` names Google Inc. rather than Google LLC in its third clause, and
is otherwise the text below.

```
Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google LLC nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
```

---

## Agents Flockdeck runs

Flockdeck starts other people's programs — Claude Code, Codex, Gemini CLI,
Aider, opencode, Cursor's agent — as separate processes, and talks to model
APIs over HTTP. None of them are included in, linked into or redistributed with
this software, and each remains subject to its own licence and terms of
service. Installing them is the user's business and their terms are between the
user and their author.
