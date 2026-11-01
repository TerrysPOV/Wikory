# Fault log: A17 transcript slice decoder

Run with `scripts/inject.sh`, one site at a time, 2026-11-01.

| # | Fault | Predicted failure | Verdict |
|---|---|---|---|
| A17-1 | a partial trailing line is consumed | `TestReadSliceSynthetic`: `End=689, want 644 (the partial last line must be left unread)` | RED as predicted |
| A17-2 | tool use offsets not recorded | `TestReadSliceSynthetic`: `tool use offsets out of order` | RED as predicted |
| A17-3 | assistant lines do not inherit the prompt identity | `TestReadSliceTracksPromptIdentityAndDropsMetaLines`: `prompt identity not inherited` | RED as predicted |
| A17-4 | hook continuation lines (`isMeta`) decoded as turn content | `TestReadSliceTracksPromptIdentityAndDropsMetaLines`: `meta or non-conversational lines were decoded as content` | RED as predicted |
