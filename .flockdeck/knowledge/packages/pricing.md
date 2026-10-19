---
type: Package
title: pricing
description: The single table of per-model token prices, with the day and source each rate was read.
resource: internal/pricing
tags: [cost, models]
timestamp: 2026-10-19T00:00:00Z
code_paths: [internal/pricing/pricing.go]
---
Only place the app states a price. Used by `internal/chat`, `internal/route`, `internal/server`.

| Name | Role |
|---|---|
| `Rate` | Dollars per million tokens: `In`, `Out`, `CacheRead`, optional long-prompt rates (`Above`, `LongIn`, `LongOut`, `LongRead`), `From`/`Until` dates, `Checked`, `Source`. |
| `Lookup(model, day)` | Matches the id exactly or as a dated snapshot (`-YYYYMMDD`, `-YYYY-MM-DD`, `@YYYYMMDD`); strips a `models/` prefix; lower-cases. An id that merely starts like a priced one gets no price. |
| `Cost`, `CostOn`, `Rate.Cost` | Dollars for a `Usage{In, Out, CacheRead, CacheWrite}`. |
| `Table()` | Copy of all entries. |

Rules in `Rate.Cost`: cache read defaults to one tenth of input; cache write is 1.25 x input; above `Above` input tokens the long rates apply. The table covers Anthropic, OpenAI and Google first-party prices only; unknown models show token counts without money. Dated entries encode announced changes (Gemini Flash rates change on 2027-01-01). A test fails a release whose table was read too long ago. The table was last read on 2026-09-12.
