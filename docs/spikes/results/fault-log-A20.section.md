| A20-1 | removed pages kept | RED as predicted (`a removed page stayed in the index: {Pages:1 Changed:0 Removed:0 Refused:0 Malformed:0}`) |
| A20-2 | walk collects non-regular `.md` entries | SURVIVED (predicted): the confined read in `Refresh` still refuses the symlink, so `Refused` stays 2; the read guard is proven to fail in A3 |
| A20-3 | joined form not emitted | RED as predicted (`Expand("enqueueJob")=" enqueue job " lacks "enqueuejob"`) |
| A20-4 | page cap not applied in the walk | RED as predicted (`page cap not enforced or not reported`) |
| A20-5 | total byte cap not applied | RED as predicted (`byte cap not enforced or not reported`) |
| A20-6 | depth cap not applied | RED as predicted (`depth cap not enforced or not reported`) |
| A20-7 | removals run after a walk cut short by a limit | RED as predicted (`a partial walk deleted indexed pages`) |
| A20-8 | Partial flag never set | RED as predicted (`page cap not enforced or not reported`) |
| A20-9 | a subdirectory error aborts the refresh | RED as predicted (`one bad entry aborted the refresh: openat locked: permission denied`) |
