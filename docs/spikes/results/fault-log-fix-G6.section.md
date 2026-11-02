| G6-1a | `isCompleteClaimComment` reverts to the old prefix-only test (`return IsClaimComment(s)`), so an unclosed `<!-- claim broken` line skips the comment scan | RED as predicted (`"PAYLOAD" reached the indexed body`) |
| G6-1b | the `Count(s, "<!--") == 1` term dropped from `isCompleteClaimComment` (a first run with the trailing-opener input SURVIVED, because that line fails the suffix test anyway; `TestClaimLookingLineWithNestedOpenerMakesNoBlock` was added) | RED as predicted (`a line with a nested opener made a claim block`) |
| G6-1c | the `HasSuffix(s, "-->")` term dropped | RED as predicted (`a claim comment with trailing text made a block`) |
| G6-1d | `commentRefDef` result ignored (`isHidden && false`): `[//]: # (text)` lines reach the body | RED as predicted (`a [//]: comment reached the body`) |
| G6-1e | a multi-line `[//]: # (...)` title not continued (`return true, 0`) | RED as predicted (`a [//]: comment reached the body`, with `HIDDEN-FIVE line two)` left over) |
