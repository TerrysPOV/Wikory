## Fault log: Task A7

| # | Fault | Predicted failure | Verdict |
|---|---|---|---|
| A7-1 | store matched on project id alone | `TestForkGetsSeparateStore`: `a fork must not find the original` | RED as predicted |
| A7-2 | checkout id ignores the worktree | `TestWorktreesAreSeparateCheckouts`: `a second worktree must not inherit the first worktree` | RED as predicted |
| A7-3 | finding a store registers the checkout | `TestCopiedOriginResolvesButDoesNotRegister`: `an unregistered checkout must stay unregistered: ok=true err=<nil>` | RED as predicted |
| A7-4 | umask not restricted | `TestStoreIsOwnerOnlyUnderPermissiveUmask`: `has mode -rw-r--r--` | RED as predicted |
| A7-5 | newer schema accepted | `TestNewerSchemaIsRefused`: `want ErrNewerSchema` | RED as predicted |
