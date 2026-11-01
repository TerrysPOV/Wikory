| A20-1 | removed pages kept | RED as predicted (`a removed page stayed in the index: {Pages:1 Changed:0 Removed:0 Refused:0 Malformed:0}`) |
| A20-2 | walk collects non-regular `.md` entries | SURVIVED (predicted): the confined read in `Refresh` still refuses the symlink, so `Refused` stays 2; the read guard is proven to fail in A3 |
| A20-3 | joined form not emitted | RED as predicted (`Expand("enqueueJob")=" enqueue job " lacks "enqueuejob"`) |
