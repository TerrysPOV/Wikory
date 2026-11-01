| A3-1 | directory components opened without `O_NOFOLLOW` | RED as predicted (`a symlink to the moved original directory was followed`) |
| A3-2 | the final name opened without `O_NOFOLLOW` | RED as predicted (`a symlink to the moved original file was followed`) |
| A3-3 | the root's final component opened without `O_NOFOLLOW` | RED as predicted (`a root replaced by a symlink to the moved original was opened`) |
| A3-4 | `ENOTDIR` at a symlink not classified as a symlink (macOS run) | RED as predicted (`want ErrSymlink`) |
| A3-5 | final open re-resolved by path from the root instead of the held directory handle | RED as predicted (`the swap redirected the open outside the root`) |
| A3-6 | non-regular files accepted after the open | RED as predicted (`want ErrNotRegular`) |
| A3-7 | `..` accepted by `Split` | RED as predicted (`want ErrEscape`) |
| A3-8 | atomic write does not refuse a symlink at the target | RED as predicted (`want ErrSymlink`) |
| A3-9 | mode left to the umask | RED as predicted (`mode=-rw------- err=<nil>`) |
| A3-10 | root identity supplement ignored | RED as predicted (`a root replaced between check and open must be refused`) |
| A3-11 | backslash accepted in a name | RED as predicted (`want ErrEscape`) |
| A3-12 | staged file installed by rename instead of a no-clobber link (edit after move-aside) | RED as predicted (`human edit not kept`) |
| A3-13 | same fault, edit made after the compare | RED as predicted (`human edit not kept`) |
| A3-14 | the moved-aside content is not judged | RED as predicted (`content="NEW"`) |
| A3-15 | recovery does not put back a backup whose target is missing | RED as predicted (`content=""`) |
| A3-16 | hard-link count check removed from the open path (read of a hard link to an outside file) | RED as predicted (`want ErrHardlink and no bytes`) |
| A3-17 | same fault, append through the hard link (also modified the outside file before the fix) | RED as predicted (`append through a hard link: want ErrHardlink`) |
| A3-18 | same fault, replace of a hard-linked target | RED as predicted (`replace of a hard-linked file: want ErrHardlink`) |
| A3-19 | `ReplaceIfUnchanged` does not propagate `ErrHardlink` from the moved-aside open (returns `ErrChanged` only) | RED as predicted (`replace of a hard-linked file: want ErrHardlink`) |
