# Third-party notices

The files in `web/app/vendor/` are builds of the `@xterm/xterm` and
`@xterm/addon-fit` packages, vendored rather than fetched at build time. They
are compiled into every relay that serves this client, so their notice travels
with it and is reproduced here.

## xterm.js

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

## Fonts

The files in `web/app/fonts/` are subsets of two typefaces, the same two the
flockdeck.ai site sets itself in, served from the relay rather than from a font
service. Both are licensed under the SIL Open Font License, Version 1.1, whose
full text is beside each file:

- **IBM Plex Sans** (`ibm-plex-sans.woff2`) — Copyright © 2017 IBM Corp. with
  Reserved Font Name "Plex" (https://github.com/IBM/plex). Licence:
  `OFL-IBMPlexSans.txt`.
- **JetBrains Mono** (`jetbrains-mono.woff2`) — Copyright 2020 The JetBrains
  Mono Project Authors (https://github.com/JetBrains/JetBrainsMono). Licence:
  `OFL-JetBrainsMono.txt`.

The outlines are unchanged. Each file is the variable font cut down to the
characters the client draws (Latin, general punctuation, arrows and the
geometric shapes) and compressed to WOFF 2; the Plex Sans file is also pinned
to its normal width. They are distributed alongside the client, not sold on
their own, as the licence asks.

## Icons

The icons in `web/app/icons/` are Flockdeck's own, under this repository's
licence.
