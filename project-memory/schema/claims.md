# Claim format (for people writing pages by hand)

Each claim is one paragraph preceded by one metadata comment line:

    <!-- claim kind=current-impl status=verified ev=src/queue.go dep=src/queue.go#Enqueue -->
    Jobs are acknowledged only after the handler returns, so a crash replays the job.

kind is current-impl, intent, rationale or hypothesis. status is verified, unverified,
conflicting, historical or superseded. ev lists evidence (a committed path, commit:<sha>),
dep lists the paths the claim depends on. Separate list items with ";".

After writing or editing claims, run wikory page stamp <path>. Wikory then mints the id,
resolves evidence to committed blob ids, computes the text hash and writes the metadata.
A model never writes this line, and neither should you beyond kind, status, ev and dep.
