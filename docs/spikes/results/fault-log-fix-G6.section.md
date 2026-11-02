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
| G6-1n | superseded by G6-1t, G6-1v and G6-1u: the character-set budget guard was replaced by a per-line container-depth count | SUPERSEDED |
| G6-1o | inline comments no longer cut from a claim's paragraph | RED as predicted (`Before <!-- HIDDEN-C --> after,` in the claim text) |
| G6-1p | claim line numbers ignore the frontmatter offset | RED as predicted (`Line:4`, want 7) |
| G6-1q | backslash escapes no longer resolved in the body | RED as predicted (`"Escaped * star and & entity." missing from body`) |
| G6-3a | `RebindClone` no longer purges the old checkouts' files after the commit | RED as predicted (`turns/<id> survived a rebind that told the user the data was discarded`; `jobs/job-old survived ...`) |
| G6-3b | the `turns/<oldCheckoutID>` removal dropped (jobs still purged) | RED as predicted (`turns/<id> survived ...`) |
| G6-3c | every job directory with a manifest is removed, not only those naming an old checkout id | RED as predicted (`jobs/job-other belongs to something else and was removed`) |
| G6-3d | removal failures not reported as a notice | RED as predicted (`no notice about the file that stayed`) |
| G6-1r | the claim-paragraph exclusion (`hiddenCarrier`) disabled | RED as predicted (`the exclusion was silent`) |
| G6-1s | claim text taken from the paragraph's raw source instead of its visible text | RED as predicted (`visible text of a plain claim paragraph`) |
| G6-1t | ordered list markers not counted as container openers (the bypass the nesting guard had) | RED as predicted (`ordered markers: finding=false`); the old character-set guard also failed this test before the fix |
| G6-1u | the 256 KiB parse cap disabled | RED as predicted (`no finding`) |
| G6-1v | indentation not counted towards nesting | RED as predicted (`indentation: finding=false`) |
| G6-1w | inline HTML no longer treated as a hidden-text carrier in a claim paragraph | RED as predicted (`the exclusion was silent`) |
| G6-1x | the O(n*m) byte-by-byte cut loop in `rawParagraph` | not re-injectable: the loop was deleted, because claim text no longer comes from raw source. Before the fix `TestManyInlineCutsInAClaimParagraphAreBounded` failed on its block assertion after 3.3 s for 20,000 cuts (under its generous 5 s time limit on this machine), so the time bound is a backstop and the structural fix is the deletion |
| G6-4 | `ensureCloneID` no longer takes the clone-setup lock (`unlock, err := func() {}, error(nil)`) | RED as predicted (`the two setups returned ... but the clone's id is ...`: two ids issued for one clone). Leaving the lock held instead is INCONCLUSIVE by design: the second setup times out after 10 s with `store: lock not acquired in time` |
| G6-5a | the url-credentials password group back to `[^\s/@]+` (stops at the first `/` or `@`) | RED as predicted (`pw 11 chars: out="dsn postgres://app_user:Ab/cd+ef9Gh@..."`, no redaction) |
| G6-5b | the multi-word rule takes only the first word (`[^\s]{7,199}`) | RED as predicted (`"horse" survived in ...`) |
| G6-5c | the 200-character bound raised to 1000 | RED as predicted (`unbounded or missed`) |
| G6-5d | a value already replaced is replaced and counted again | RED as predicted (`counts=map[assignment:2]`). A first version of the test also asserted a single marker, which cannot fail (the second pass rewrites a marker into the same marker); it was removed |
| G6-5e | the identifier-tail restriction dropped (any letters allowed in the key tail) | RED as predicted (`prose mangled: "Use a password manager. Passwords: [REDACTED:assignment]"`) |
| G6-6a | the duplicate check ignores log.1.md | RED as predicted (`a job line that lives in log.1.md was appended again to log.md (1 copies)`) |
| G6-6b | rotation attempted even though log.1.md is not a regular file | RED as predicted (`a blocked rotation failed the publish: err=renameat log.md log.1.md: is a directory`) |
| G6-6c | the refusal is not reported as a notice | RED as predicted (`no notice that the log could not be rotated`). `Publisher.Notices` is new; the app does not print it yet (the app's publisher construction is outside this wave's files), so today the notice is available to callers and tests only |
