| G1-1 | a hostile `.git` file (absolute or relative gitdir) inherits the trusted clone's trust: the Confirmed gate in `load` removed | RED as predicted (`was served its memory`) |
| G1-1b | `Discover` reports every worktree as confirmed | RED as predicted (`hostile main: Confirmed=true, want false`) |
| G1-1c | `wikory init` registers an unconfirmed worktree | RED as predicted (`init in a hostile directory: code=0`) |
| G1-2 | `core.worktree` outside the current directory is followed | RED as predicted (`want ErrWorktreeRedirected`) |
| G1-2b | the redirect error is not surfaced by `discover`, so init reports "not inside a git repository" | RED as predicted (`init with core.worktree: code=2 err=wikory init: not inside a git repository`) |
| G1-3 | `findStore` matches origin and common dir in one pass, so a late-added remote flips a clone onto another store | RED as predicted (`round 8: the clone was flipped onto another clone's store`) |
| G1-3b | `Resolve` copies an origin another store owns into this store's registry | RED as predicted (`origins=[github.com/o/r] notices=[]`) |
| G1-3c | `Register` copies an origin another store owns | RED as predicted (`Register aliased an origin another store owns`) |
| G1-4 | a git failure in `headRev` is returned as an unborn HEAD (`"", nil`) | RED as predicted (`HeadTree swallowed a git failure`; `a HEAD whose commit is missing read as "" with no error`) |
| G1-4b | `HeadBlobs` ignores the error from `HeadCommit` | RED as predicted (`HeadBlobs swallowed a git failure`) |
| G1-5 | the clone-id check in `load` removed | RED as predicted (`a different repository at a trusted clone's path was served its memory`) |
| G1-5b | `init` keeps the trust decision of the clone that was replaced | RED as predicted (`init handed the replaced clone's trust to the new repository`) |
| G1-5c | the clone id file is written 0644 | RED as predicted (`clone id file mode -rw-r--r--`) |
| G1-5d | `ensureCloneID` mints a new id on every init | RED as predicted (`a second init rewrote the clone id`) |
| G1-6 | the schema statements run before the version check | RED as predicted (`the schema ran against a refused store: counters table present=1`) |
| G1-6b | the registry is written before the database is opened | RED as predicted (`a refused store's registry was rewritten`) |
| G1-7a | the lock file is opened without `O_NOFOLLOW` | RED as predicted (`Lock followed a symlink and locked its target`) |
| G1-7b | the regular-file check removed (a device is accepted) | RED as predicted (`Lock accepted a device as its lock file`) |
| G1-7c | the lock file is opened without `O_NONBLOCK` | SURVIVED: no portable test can observe it, because the lock is opened `O_RDWR`, which never blocks on a FIFO on Linux or macOS. The flag is defence in depth for devices that block on open |
| G1-8 | a clone with no recorded nonce is bound without revoking (taken to be the same clone) | RED as predicted (`init kept a trusted decision it could not verify`) |
| G1-9 | the nonce is bound without revoking the old trust | RED as predicted (`a failure at after-file left the path trusted`) |
| G1-9b | revocation commits, then the nonce is bound in a separate step (not one transaction) | RED as predicted (`a failed rebind changed the trust decision: "undecided" true`) |
| G1-10a | `pages` rows of the replaced clone are not deleted | RED as predicted (`2 row(s) of the old clone survive in pages`) |
| G1-10b | `job_ops` rows of the replaced clone are not deleted | RED as predicted (`5 job_ops row(s) of the old clone survive`) |
| G1-10c | the checkout id ignores the clone nonce | RED as predicted (`the replacement clone got the old checkout id`) |
| G1-10d | `turns` rows of the replaced clone are not deleted | RED as predicted (`1 row(s) of the old clone survive in turns`) |
