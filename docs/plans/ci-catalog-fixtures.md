# Riallineamento fixture CI/catalogo

- Plan-ID: jeff:ci-catalog-fixtures
- Repository: `/home/alessandro/Work/tries/jeff`
- Branch: `master`
- Worktree: `/home/alessandro/Work/tries/jeff`
- Baseline HEAD: `ca03fc4f67b5a0415d66a94dd0e121bf13c1f1c2`
- Owner session: `01a0b832-3713-7275-9c75-c3ecaa8de0ca`
- Status: complete
- Current slice: none

## Alignment

| ID | Requisito | Accettazione osservabile | Non deve cambiare |
|---|---|---|---|
| R1 | Il catalogo corrente è quello canonico | `Load()` espone esattamente `GEN001`–`GEN020` | `gen.yml`, loader e contratto funzionale |
| R2 | I fake TypeSafe rappresentano richieste complete | Ogni risposta contiene una risposta valida per ogni domanda richiesta | Validazione strict della cardinalità in `internal/typesafe` |
| R3 | La CLI conserva i propri exit code | 0 pass, 1 violation, 2 inconclusive/error | JSON e comportamento runtime |

Non-obiettivi: ridurre il catalogo a due regole, reintrodurre `SEC001`, allentare il client, modificare CI o soglie.

## Ricerca e decisioni

`internal/rules/catalog.go` carica tutti i frammenti embedded; `gen.yml` contiene 20 regole GEN. I test legacy costruiscono risposte solo per `GEN001` e `SEC001`; `SEC001` non appartiene più al catalogo. La correzione è confinata a test e fixture. I fake comportamentali genereranno risposte per le chiavi `questions` ricevute, mentre il test del catalogo manterrà la lista esplicita per rilevare cambiamenti intenzionali.

## Slices

### [x] S1 — Test del contratto catalogo

- Requirements: R1
- Files: `internal/rules/catalog_test.go`
- Implementation: sostituire l'aspettativa `2` con i codici espliciti `GEN001`–`GEN020`, mantenendo i controlli metadata e selector.
- Mandatory check: `go test ./internal/rules -count=1`
- Runtime check: NOT APPLICABLE
- Result: PASS — `go test ./internal/rules -count=1`

### [x] S2 — Fixture check/cache/context-limit

- Requirements: R1, R2
- Files: `internal/check/check_test.go`, `internal/check/cache_test.go`, `internal/check/batching_test.go`
- Implementation: aggiornare i fake per rispondere a tutte le domande; usare `GEN002` nel caso di risposta invalida/output testuale; aspettarsi 20 check/cache entry; mantenere il caso context-limit terminale.
- Not doing: modifiche a `Run`, `Client`, cache o contratto provider.
- Mandatory check: `go test ./internal/check -count=1`
- Runtime check: NOT APPLICABLE
- Result: PASS — `go test ./internal/check -count=1`

### [x] S3 — Fixture CLI

- Requirements: R2, R3
- Files: `cmd/jeff/main_test.go`
- Implementation: fare in modo che `noulServer` risponda per tutte le domande ricevute e aggiornare l'aspettativa a 20 check; conservare gli exit code esistenti.
- Mandatory check: `go test ./cmd/jeff -count=1`
- Runtime check: NOT APPLICABLE
- Result: PASS — `go test ./cmd/jeff -count=1`

## Gate finale

```sh
go vet ./...
go test ./... -count=1
```

## Rischi

- Accettare risposte parziali nel client nasconderebbe controlli mancanti: da evitare.
- Usare un catalogo ridotto nei test renderebbe verde una configurazione non reale: da evitare.
- L'elenco esplicito in S1 deve essere aggiornato insieme a ogni aggiunta intenzionale di regole.

## Requirement changes and plan deviations

- None

## Last checkpoint

- Revision: `ca03fc4f67b5a0415d66a94dd0e121bf13c1f1c2`; working tree non pulito con le cinque modifiche ai test e il piano non committato.
- Checks: `go test ./internal/rules -count=1`, `go test ./internal/check -count=1`, `go test ./cmd/jeff -count=1`, `go vet ./...` e `go test ./... -count=1` — tutti PASS.
- Blocker: nessuno.
- Next action: nessuna; pronto per review/commit.
