# Temporary immutable migration evidence

Exact files exported with `git archive` from Ori
`8c076a15bb32895c114705a1e3a2cdf72395572c`. Apache-2.0; original notices retained.
This fixture is never selected at runtime and is removed at the final extraction
gate. Original helper Go sources still remain in Ori until that gate.

For complete independent recovery, export that revision with `git archive` to a
separate directory; do not repair wrappers by sourcing this fixture implicitly.
