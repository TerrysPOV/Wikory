# S10 static inspection (no interactive session, no model call)

Claude Code 2.1.316, inspected with `claude plugin validate`, `claude plugin --help` and a text search of the installed binary. The interactive launch was not run and took no ledger slot: it would stop at, or need an answer to, the folder-trust prompt.

## V19: plugin settings.json keys

The bundle declares the only keys a plugin settings.json may supply, and builds its parser from that list:

    var A3o=["agent","subagentStatusLine"];
    tTs=f(()=>u(Object.fromEntries(A3o.map((e)=>[e,uw(e)]))));
    function fKn(e){let n=tTs().safeParse(e); if(!n.success)return; ...

`statusLine` is not in the list. The spike plugin's settings.json carries both `statusLine` and `subagentStatusLine`. `claude plugin validate` on the spike plugin passed and said nothing about either key, so validate does not check plugin settings keys. Runtime behaviour (that the status line is not rendered) was not observed.

## V18: /clear and SessionStart

The SessionStart payload schema lists the sources `startup`, `resume`, `clear`, `compact`, `fork`, and the /clear handler runs the SessionStart hooks with source `clear` (the code logs "SessionStart hooks after /clear aborted" on abort). A real `/clear` was not observed.
