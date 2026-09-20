# Default input and transport safety

- Plan-ID: jeff:security-boundary
- Repository: `/home/alessandro/Work/tries/jeff`
- Branch: `master`
- Worktree: `/home/alessandro/Work/tries/jeff`
- Baseline HEAD: `c83513c5a8d7e5ae9e6bc9b9beefc8e9c071523c`
- Status: complete
- Current slice: none

## Alignment

| ID | Requisito | Accettazione osservabile | Must not change |
|---|---|---|---|
| R1 | Il default non deve inviare dati non-sorgente | Le regole embedded non si applicano a certificati, config/data, dotfile/directory nascoste o directory escluse; sorgenti comuni restano controllabili anche con path esplicito | Le regole esterne possono optare esplicitamente per file non-sorgente |
| R2 | La cache deve risultare ignorata senza intervento dell'utente | La prima scrittura crea un `.gitignore` nella directory della cache che ignora le entry | Formato delle entry e `--no-cache` |
| R3 | Documentazione coerente | Il riferimento Jev inesistente viene rimosso e il README descrive la policy | CLI e protocollo TypeSafe |
| R4 | Nessun bearer token su HTTP remoto | `http://` è accettato solo per loopback; gli altri endpoint richiedono HTTPS | Mock locali con `httptest` |

Non-goal: secret scanning dentro file sorgente; nuovi flag CLI; cambi al protocollo o al formato della cache.

## Slices

### [x] S1 — Source allowlist e opt-in non-sorgente
- Requirements: R1
- Files: `internal/files`, `internal/rules`, tests.
- Check: `go test ./internal/files ./internal/rules ./internal/check -count=1`
- Result: PASS — source allowlist, hidden/default-excluded path rejection, and explicit `allow-non-source` opt-in tested.

### [x] S2 — Cache self-ignored
- Requirements: R2
- Files: `internal/cache`, cache tests.
- Check: `go test ./internal/cache ./internal/check -count=1`
- Result: PASS — cache writes `v1/.gitignore` with `*` before entries.

### [x] S3 — HTTPS e documentazione
- Requirements: R3, R4
- Files: `internal/typesafe`, tests, `README.md`.
- Check: `go vet ./... && go test ./... -count=1 && git diff --check`
- Result: PASS — loopback HTTP remains available for tests; remote HTTP is rejected; README updated and broken Jev link removed.

## Requirement changes and plan deviations

- None.

## Last checkpoint

- Revision: implementation complete; worktree was already dirty before this change.
- Checks: `go vet ./...`, `go test ./... -count=1`, `git diff --check`, localhost mock upload/cache smoke — PASS.
- Blocker: none.
- Next action: none.
