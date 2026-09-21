## Why

<!-- The reason this change exists, in a sentence or three. Skip what changed —
     the diff already says that. What the diff can't say is what was wrong, or
     what became possible. If it fixes a reported issue, link it. -->

## Testing

<!-- What you actually ran. `go tool task ci` is often the whole answer.
     If the change can't be covered by a test, say what you did instead. -->

<!-- ----------------------------------------------------------------------
Every commit here lands on `main` exactly as written. Merge commits are the
only permitted merge method, so nothing gets squashed or rewritten, and your
SHAs and signatures survive. Group the work into whatever commits make sense —
more than one is fine.

That also means each commit subject has to be a Conventional Commit, because
those are what drive the changelog and the next version number:

    fix: deletes no longer strand a shadow when the source is gone
    feat(cli): add --dry-run to wipe
    docs: correct the cosign image tag

Breaking the rules.yaml schema, the chart values or the CLI means `feat!:` or
a breaking-change footer, and that is a major version now that 1.0.0 has
shipped. The checks print the exact version this pull request would produce,
so there is no need to guess.
------------------------------------------------------------------------- -->
