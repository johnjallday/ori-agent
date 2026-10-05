# Away Dispatcher — external operating guide

Read `docs/away-dispatcher.md` and `docs/cutover.md` in the selected Ori devtools
source. [Source selection and recovery](devtools.md) explains how to locate it.
Ori retains legacy dispatcher/tick/support paths as thin compatibility shims;
installed jobs do not need interactive shell startup files.

Queue authorization, model intent, dependencies/overlap guards and the distinct
`com.ori.wt-away-tick` ownership remain unchanged. Do not cancel jobs, edit an
installed helper, install capabilities or reset queues to test source relocation.
An interactive source override does not change an installed job's environment.
Offline fixtures do not establish live unattended-operation readiness.
