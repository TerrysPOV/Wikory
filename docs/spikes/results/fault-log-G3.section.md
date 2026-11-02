# Fault log: G3 review-fix wave (sanitiser, redactor, page comments, claim encoding, validator status)

Run with `scripts/inject.sh`, one site at a time, against the committed G3 code. Each row was predicted RED.

| # | Fault | Verdict |
|---|---|---|
| G3-1a | sanitiser no longer strips Unicode category Cf (tag characters, U+061C, U+180E) | RED as predicted (`hidden tag characters survived`) |
| G3-1b | variation selector range U+FE00 starts at U+FE01 | RED as predicted (`variation selector 1 (U+FE00) survived`) |
| G3-1c | NFKC replaced by NFC | RED as predicted (fullwidth and mathematical spellings of `system` left as `&lt;...&gt;` text instead of being stripped) |
| G3-1d | angle-bracket look-alike mapping (mathematical and CJK brackets) removed | RED as predicted (`<system>` written with U+3008 and U+3009 survives as text) |
| G3-2a | key rule loses the identifier tail (`AWS_SECRET_ACCESS_KEY=`) | RED as predicted (`AWS_SECRET_ACCESS_KEY: out=` unredacted) |
| G3-2b | key rule gets its leading word boundary back (`DB_PASSWORD=`) | RED as predicted (`DB_PASSWORD: out=` unredacted) |
| G3-2c | URL userinfo password group can no longer match | RED as predicted (`counts=map[]`, password still in the URL) |
| G3-2d | Basic-scheme rule never matches | RED as predicted (`the scheme name should stay`) |
| G3-2e | double-quoted values with spaces no longer match | RED as predicted (`correct horse battery` survives) |
| G3-2f | separator made optional (false-positive control) | RED as predicted (`prose mangled: "This token bucket refills; see the api-key rotation runbook...`) |
| G3-2g | quoted value runs to the last quote on the line (the first version of the test SURVIVED because the line held one quote pair; a second quoted phrase was added) | RED as predicted (`text after the closing quote was swallowed`) |
| G3-3a | text after a closing marker on the same line dropped | RED as predicted (`body: "next line"`) |
| G3-3b | an unterminated comment no longer stays open (the first prediction text was wrong; the opener's trailing text comes first) | RED as predicted (`body: "Visible.\n never closed\nHIDDEN: ...`) |
| G3-3c | a comment inside a claim paragraph ends the paragraph | RED as predicted (`leaked into it, or split it`) |
| G3-3d | claim comment lines are no longer protected from comment removal | RED as predicted (`blocks: []`) |
| G3-3e | comment state not carried into the next line | RED as predicted (`"HIDDEN" survived in body`) |
| G3-4a | comma left raw by `encodeItem` | RED as predicted (`",": rendered line does not parse`) |
| G3-4b | `%` left raw by `encodeItem` | RED as predicted (`"%2C": round trip changed the claim`) |
| G3-4c | `@` left raw by `encodeItem` | RED as predicted (`"@": rendered line does not parse`) |
| G3-4d | `;` left raw by `encodeItem` | RED as predicted (`";": rendered line does not parse`) |
| G3-4e | `renderChecked` stops refusing a line that does not re-parse | RED as predicted (`line="<!-- claim kind=intent status=unverified aka=\"\" -->" err=<nil>`) |
| G3-5 | `Derive` shows `hypothesis` as a status again | RED as predicted (`Status:hypothesis`) |

Not injectable: the call from `stampPage` to `renderChecked`. No claim that `ParseComment` accepts can render to a line it rejects once `encodeItem` is fixed, so no test can reach the refusal branch through the command. `renderChecked` itself is covered by G3-4e; the wiring is defensive.
