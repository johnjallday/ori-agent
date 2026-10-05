# Herdr Wake Service dogfood — source moved

The full manual protocol is `docs/herdr-standalone-wake-dogfood.md` in the
selected Ori devtools source. Read [selection and recovery](devtools.md) and the
[accepted contract location](architecture/herdr-standalone-wake-v1-contract.md).

Do not infer live readiness from source builds, fake platform tests or
cross-compilation. Installation, administrator approval, wake scheduling,
self-test, sleep/resume and uninstall each retain their explicit authorization
and evidence requirements. Preserve existing jobs and foreign wake events.
