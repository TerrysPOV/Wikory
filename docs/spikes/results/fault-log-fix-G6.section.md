| G6-1a | `isCompleteClaimComment` reverts to the old prefix-only test (`return IsClaimComment(s)`), so an unclosed `<!-- claim broken` line skips the comment scan | RED as predicted (`"PAYLOAD" reached the indexed body`) |
| G6-1b | the `Count(s, "<!--") == 1` term dropped from `isCompleteClaimComment` (a first run with the trailing-opener input SURVIVED, because that line fails the suffix test anyway; `TestClaimLookingLineWithNestedOpenerMakesNoBlock` was added) | RED as predicted (`a line with a nested opener made a claim block`) |
| G6-1c | the `HasSuffix(s, "-->")` term dropped | RED as predicted (`a claim comment with trailing text made a block`) |
| G6-1d | the over-strip of link-definition-like lines disabled (`defLikeRe.MatchString(lines[i]) && false`); replaces the earlier `[//]:`-only recogniser, which the orchestrator ruled out as a parser-differential risk (escaped title delimiters, a title opening on the next line) | RED as predicted (`a payload behind a link definition reached the body`; `a [//]: comment reached the body`) |
| G6-1e | the dropped run ends after one line (`else if inDef` without the blank-line test), so a payload line after an escaped title delimiter is indexed | RED as predicted (`a payload behind a link definition reached the body`) |
| G6-1f | comment markers inside a dropped run are no longer scanned (`_ = scanComments(...)`) | RED as predicted (`body: "PAYLOAD-AFTER-BLANK ...`) |
| G6-1g | the finding for a dropped run is not recorded | RED as predicted (`no finding for the dropped run`) |
| G6-2a | `git worktree list` run without `-z` (newline-separated porcelain), so a worktree directory name holding a newline can forge an entry | RED as predicted (`want exactly 2 entries (main and the newline-named one), got 1`). The Discover-level forged-line test alone SURVIVES this fault, because the binding check (G6-2c to G6-2f) also refuses the forged directory; the parser-level test covers the `-z` layer |
| G6-2b | prunable entries kept (`!prunable` made unconditional) | RED as predicted (`got ["/a" "/gone" "/b" "/bare-prunable"], want [/a /b]`) |
| G6-2c | the "entry is directly under `<common>/worktrees`" test disabled | RED as predicted (`a git directory outside <common>/worktrees was accepted`) |
| G6-2d | the `.git` file is no longer required to name the git directory | RED as predicted (`a .git file naming a different directory was accepted`) |
| G6-2e | the entry's `gitdir` file is no longer required to name `top/.git` | RED as predicted (`an entry whose gitdir file names another directory was accepted`) |
| G6-2f | `confirm` returns true for any listed path (no binding check at all) | RED as predicted (`a worktree whose .git file no longer names its own entry was confirmed`) |
| G6-1* | rows G6-1a to G6-1g above fault the hand-written comment and definition stripper, which was deleted when the body moved to a CommonMark parse (orchestrator ruling); they are kept as history and are SUPERSEDED by rows G6-1h to G6-1q | SUPERSEDED |
| G6-1h | the body builder replaced by raw page text (`w.add(string(w.src))`) | RED as predicted (`hidden payload reached the body`, for the comment, unterminated comment and half-open claim shapes) |
| G6-1i | HTML blocks added to the body | RED as predicted (`hidden payload reached the body`) |
| G6-1j | raw inline HTML added to the body | RED as predicted (`hidden payload reached the body`, inline comment shapes) |
| G6-1k | image alt text added to the body | RED as predicted (`HIDDEN-16 alt`) |
| G6-1l | the top-level-only test for a claim dropped | RED as predicted (`in a blockquote: made a claim`). It SURVIVED the first version of the test, whose fixture left the claim text outside the quote; the fixture now keeps it inside |
| G6-1m | the complete-comment test for a claim line dropped | RED as predicted (`nested opener: made a claim block`, and the two other shapes) |
| G6-1n | the nesting budget disabled | RED as predicted (`quotes: finding=false`, `list markers: finding=false`); unguarded, a 1 MiB page of quote marks takes 9 s to parse and one of list markers 18 s |
| G6-1o | inline comments no longer cut from a claim's paragraph | RED as predicted (`Before <!-- HIDDEN-C --> after,` in the claim text) |
| G6-1p | claim line numbers ignore the frontmatter offset | RED as predicted (`Line:4`, want 7) |
| G6-1q | backslash escapes no longer resolved in the body | RED as predicted (`"Escaped * star and & entity." missing from body`) |
| G6-3a | `RebindClone` no longer purges the old checkouts' files after the commit | RED as predicted (`turns/<id> survived a rebind that told the user the data was discarded`; `jobs/job-old survived ...`) |
| G6-3b | the `turns/<oldCheckoutID>` removal dropped (jobs still purged) | RED as predicted (`turns/<id> survived ...`) |
| G6-3c | every job directory with a manifest is removed, not only those naming an old checkout id | RED as predicted (`jobs/job-other belongs to something else and was removed`) |
| G6-3d | removal failures not reported as a notice | RED as predicted (`no notice about the file that stayed`) |
