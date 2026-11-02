| G8-1a | the `[` and `](` bound (1,500) raised 100 times | RED as predicted (`brackets 1501: finding=false`) |
| G8-1b | the `*`/`_` bound (4,000) raised 100 times | RED as predicted (`delimiters 4001: finding=false`) |
| G8-1c | the backtick bound (2,000) raised 100 times | RED as predicted (`backticks 2001: finding=false`) |
| G8-1d | the `](` count dropped from the check | RED as predicted (`close-open 1501: finding=false`) |
| G8-1e | the parse cap raised from 64 KiB to 256 KiB (the fixture is 93 KB, so it now parses in full) | RED as predicted (`"EARLY-PARAGRAPH" in a headings-only body`) |
| G8-2a | paragraphs holding raw inline HTML are indexed again | RED as predicted (`hidden text reached the index`: paragraph, list item and quote shapes) |
| G8-2b | headings holding raw inline HTML are indexed again | RED as predicted (`atx heading` and `setext heading` shapes) |
| G8-6 | entities and escapes decoded inside code spans again | RED as predicted (`body "Use <x> and * here and <y> outside."`) |
| G8-3 | the url-credentials username back to `+` (at least one character) | RED as predicted (`out="cache redis://:...@cache.internal:6379 end"`, no redaction) |
| G8-5a | `schemaComplete` never reports a missing object (the repair runs without the lock again) | RED as predicted (`want ErrWriteLockTimeout, got <nil>`) |
| G8-5b | `schemaComplete` always reports incomplete (every open takes the lock) | RED as predicted (`a complete database should open while another process holds the lock`) |
| G8-4a | superseded form (value may not start with `=`, `~`, `>`) restored in the multi-word rule | RED as predicted (`a secret behind an operator character survived: "token=>correct horse battery staple"`) |
| G8-4b | the same exclusion restored in the single-word rule | RED as predicted (`a secret behind an operator character survived`) |
| G8-4c | the comparison group is detected but not honoured (`false && ...`) | RED as predicted (`a comparison was rewritten: "if password == expected_value_here"`) |
| G8-4d | the comparison operators need no following whitespace | RED as predicted (`a secret behind an operator character survived: "token=>correct ..."`) |
| G8-4e | a skipped comparison is still counted | RED as predicted (`map[assignment:2]`) |
| G8-4f | the `:=` operator removed from the multi-word rule | covered by the earlier row of the same name; the test `tk := correct horse battery staple` is unchanged |
