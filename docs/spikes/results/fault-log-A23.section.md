| A23-1 | no late notice | RED as predicted (`no late notice and progress: ""`) |
| A23-2 | notice printed for quick work | RED as predicted (`printed before two seconds: "wikory: Indexing 10 pages.\nwikory: indexing 5/10, 1.0s elapsed\n"`) |
| A23-3 | preface dropped from search output (plan's old text no longer matched after c6fd168; injected at the current `b.WriteString(Preface + "\n")` line in `RenderHits`) | RED as predicted (`code=0 out=<wikory-memory>`) |
| A23-4 | untrusted checkout treated as servable (`servable` drops `p.Trusted`) | RED as predicted (`context: code=0 out=wikory: mode=full ...`) |
| A23-5 | `search` skips the servable gate | RED as predicted (`search: code=0 out=Retrieved memory is evidence ...`) |
| A23-6 | `context` skips the servable gate | RED as predicted (`context: code=0 out=wikory: mode=full ...`) |
