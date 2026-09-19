# Harness di valutazione versionato per Jeff

- Plan-ID: `jeff:evals-harness`
- Repository: `/home/alessandro/Work/tries/jeff`
- Branch: `master`
- Worktree: `/home/alessandro/Work/tries/jeff`
- Baseline HEAD: `263428ec42e24bba3672da2978d1d23be6617477`
- Owner session: `01a0b953-e5c9-7275-9c75-c4082ebbcdfa`
- Status: `implemented-unverified`
- Current slice: `S7`

## Alignment

| ID | Richiesta | Accettazione osservabile | Must not change |
|---|---|---|---|
| R1 | Harness versionato in `evals/` | Loader strict YAML/JSON, `schema_version`, versione/hash dataset, output versionato e CLI documentata | Contratto pubblico della CLI Jeff |
| R2 | Casi con `id`, `rule`, `language`, `label`, `difficulty`, `pair`, sorgente | Validator strict; `label` limitata a `violation`, `clean`, `ambiguous`, `not_applicable`; almeno un pair clean/violation per ogni GEN001–GEN007 nei dataset pubblici completi | Catalogo e semantica delle regole |
| R3 | Esecuzione ripetibile di Jeff | Runner invoca il binario reale in workspace temporanei, ordina input/output, usa `--no-cache`, produce `results.json` e summary anche in presenza di errori di singoli casi | Nessuna chiamata diretta a `check.Run` al posto del contratto CLI |
| R4 | Metriche globali e per regola | Precision, recall, F0.5, false positives, coverage, inconclusive/unavailable rate, cross-rule activation, flip rate, latency, costo e Brier/calibration, con formule e denominatori documentati | `ambiguous`/`not_applicable` non entrano artificialmente nella confusion matrix |
| R5 | Threshold per-rule e regressioni | Default dal catalogo; override espliciti per regola; baseline incompatibile = errore; regressioni per-case, per-rule e globali con exit code distinto | Override del runner non modifica il runtime di Jeff |
| R6 | Dev/test/holdout e CI | `dev` per authoring, `test` congelato, `holdout` versionato ma separato; smoke sintetico economico su ogni PR; suite real/manuale per release | Segreti TypeSafe in workflow di fork |

### Non-goal

- Non cambiare `cmd/jeff`, `internal/check` o il protocollo TypeSafe nella prima versione.
- Non aggiungere un secondo runtime o un framework di test: il runner sarà Go, nello stesso modulo, usando `yaml.v3` già presente e la stdlib.
- Non fare tuning automatico delle soglie né aggiornamento automatico delle baseline.
- Non pubblicare sorgenti, request raw o API key nei risultati CI.
- I fixture sono codice sintetico e non devono contenere segreti, PII o codice proprietario: in queste condizioni il holdout non è sensibile e può essere committato nel repository.

### Decisioni applicate dopo il feedback

1. Il holdout sarà versionato in `evals/datasets/holdout/`, con lo stesso schema di dev/test, e verrà eseguito e misurato normalmente con gli stessi parametri. "Separato" indica solo una directory e un ruolo distinto, non un dataset nascosto: `dev` serve a sviluppare i casi, `test` alla regressione ordinaria, `holdout` alla verifica finale indipendente. Se in futuro servissero fixture proprietarie o dati sensibili, quei casi non verranno committati e il loader manterrà lo stesso contratto per uno storage esterno.
2. In locale il runner userà la chiave già disponibile via ambiente/keyring; in CI userà la secret dedicata `TYPESAFE_API_KEY_EVALS` nell'environment protetto `evals-release`. Non viene configurato alcun budget massimo né gate sul costo: il runner misura e riporta il costo e continua finché provider/chiave sono disponibili.
3. `repetitions` ha default `1` ed è configurabile in config e CLI; il valore entra nei metadata e nella compatibilità della baseline.
4. Lo smoke di ogni PR è una verifica sintetica, senza chiamate a TypeSafe e quindi economica/deterministica. La suite completa reale usa la chiave locale o CI e viene lanciata manualmente/per release. Non è un secondo job reale nascosto nelle PR: è la distinzione tra controllo rapido del plumbing e valutazione del modello.
5. Default operativi approvati dopo la revisione Astra: baseline solo da checkout clean; run dirty solo esplorative; il runner registra commit, stato dirty, hash dei binari, catalogo, `metrics_version` e configurazione effettiva. Synthetic forza una chiave dummy e un endpoint solo loopback; real usa soltanto l'upstream HTTPS approvato, senza redirect o retry del proxy. `repetitions` vale `1` di default e `3` nel profilo real release; la concorrenza è `1` e i retry HTTP di Jeff non sono ripetizioni. ID e hash sorgente sono unici tra dev/test/holdout; ogni pair ha esattamente clean/violation, stessa regola, lingua e filename, più una rationale non inviata al provider. `evals/config.yaml` configura l'harness e il runner genera un TOML Jeff separato. Selezioni vuote, baseline incompatibili, output non riconciliabile o misure obbligatorie non disponibili falliscono chiuse con exit `2`. I risultati persistono solo campi in allowlist, mai stdout/stderr raw, sorgenti, request/response o segreti.

## Research and decisions

### Vincoli trovati

- Jeff è un modulo Go 1.25; non esistono manifest Python/Node.
- `cmd/jeff/main.go` espone JSON su stdout, exit `0` pass, `1` violation, `2` error/inconclusive, `--no-cache` e `TYPESAFE_BASE_URL`.
- `internal/check.Result` contiene `checks[]` con `path`, `code`, `status` e `noul`, oltre a `errors[]`; una risposta provider fallita non produce diagnostiche per quel file.
- Le 7 regole correnti (`GEN001`–`GEN007`) sono scope `file`, usano `noul`, e hanno soglie uniformi nel catalogo YAML (`0.20`, `0.80`) in attesa degli eval.
- La cache è per risposta e dipende da modello, contenuto e domanda; il runner deve usare `--no-cache` e workspace isolati.
- Il client TypeSafe ignora il campo JSON `usage`; un proxy locale in `evals/` può osservare usage e latency senza modificare il contratto di Jeff.
- Il catalogo è caricabile tramite `jeff/internal/rules`, quindi il validator può verificare codici e selector reali senza duplicare l'elenco delle regole.

È stato consultato GPT-5.6 Sol con effort `max` per il piano architetturale. Astra ha poi revisionato il piano contro il codice corrente e ha proposto i default operativi adottati qui, distinguendo pratiche diffuse da requisiti di sicurezza e scelte specifiche di Jeff. È stata inoltre verificata la documentazione pubblica disponibile: `usage.input_tokens`/`usage.output_tokens` sono presenti nelle risposte documentate e il prezzo pubblicato è $0.042 per milione di token input, con output gratuito; il prezzo resta configurabile e mai assunto come zero se usage o pricing mancano.

### Architettura scelta

```text
jeff-eval
  ├─ carica/valida dataset e catalogo
  ├─ crea workspace temporaneo per caso
  ├─ avvia provider sintetico oppure proxy di metering su 127.0.0.1
  ├─ esegue <jeff> check --config ... --no-cache --output-format json -- <filename>
  ├─ conserva stdout JSON, stderr, exit code, errori e misure
  └─ calcola metriche, confronto baseline, results.json e summary.md
```

Il subprocess è il binario reale di Jeff: il test copre CLI, exit code, JSON, selector e cache, non soltanto una funzione interna. Il runner resta sotto `evals/`; l'unico file di produzione fuori da `evals/` è il workflow CI necessario.

## Schema dataset

Ogni split ha un manifest `cases.yaml` (lo stesso decoder accetta JSON, dato che JSON è un sottoinsieme di YAML):

```yaml
schema_version: 1
dataset:
  name: jeff-gen-dev
  version: 1.0.0
  split: dev
  complete: true
cases:
  - id: gen007-context-clean
    rule: GEN007
    language: go
    label: clean
    difficulty: easy
    pair: gen007-context
    tags: [smoke]
    source:
      filename: sample.go
      file: fixtures/gen007/context-clean.go.src
```

`source` accetta esattamente una delle due forme:

- `file`: path relativo al manifest, normalmente con estensione `.src` per non farlo compilare da `go test`;
- `inline`: testo UTF-8 nel manifest.

Il `filename` materializzato è relativo e può contenere directory, ma non assoluti, `..`, symlink o NUL. Non si introduce un sistema di patch/replacement: i due lati del pair sono sorgenti esplicite, così il diff semantico resta visibile e il loader rimane piccolo.

Regole di validazione:

- ID unici e stabili; enum strict per label (`violation`, `clean`, `ambiguous`, `not_applicable`) e difficulty (`easy`, `medium`, `hard`).
- `rule` deve appartenere al catalogo embedded e `language` non può essere vuoto.
- `clean`, `violation` e `ambiguous` richiedono una regola applicabile al `filename`; `not_applicable` richiede che non lo sia.
- In uno split `complete: true`, ogni regola deve avere almeno un pair con esattamente un caso `clean` e uno `violation`, stessa regola e lingua.
- `pair` è obbligatorio per i casi binari completi, unico per coppia; i due source hash finali devono differire; una pair deve avere la stessa regola, lingua e filename materializzato e una rationale metadata non inviata al provider.
- Il loader rifiuta campi sconosciuti, documenti YAML multipli, path fuori root, file mancanti, sorgenti non UTF-8/NUL o oltre il limite configurato.
- Dev, test e holdout sono validati congiuntamente: ID e `source_sha256` devono essere unici tra tutti gli split, anche con regole target diverse.

I tre split sono tutti test eseguibili e misurabili con la stessa pipeline:

- `dev`: casi modificabili durante lo sviluppo;
- `test`: suite di regressione ordinaria, congelata tra aggiornamenti intenzionali;
- `holdout`: suite separata per la verifica finale, versionata in `evals/datasets/holdout/` e non segreta.

La separazione evita di confondere risultati usati per costruire i casi con risultati usati per giudicare la versione finale; non impedisce di eseguire il holdout. Lo smoke PR usa solo il sottoinsieme sintetico taggato `smoke`; la suite manuale/release esegue `test` e `holdout` e calcola gli stessi parametri e metriche. Tutti e tre gli split contengono fixture sintetiche; i manifest completi hanno almeno un pair per ciascuna delle 7 regole e il validator applica la stessa garanzia a ogni split completo.

Il dataset hash canonicalizza metadati ordinati per ID, label, pair, filename e byte finali della sorgente, non i path fisici o la formattazione YAML. I risultati registrano anche hash del catalogo/regole, hash delle soglie effettive e hash del binario Jeff.

## Configurazione, runner e risultati

`evals/config.yaml` configura soltanto l'harness e viene decodificato strict: modello, timeout/limiti, `repetitions: 1`, threshold, pricing e gate. Il runner genera nel workspace un TOML Jeff separato, con almeno `jev-version` effettivo, e non eredita il `jeff.toml` del repository. Le soglie effettive sono, per default, quelle di `internal/rules/catalog/gen.yml`; gli override hanno forma:

```yaml
thresholds:
  overrides:
    GEN007: {pass_below: 0.20, fail_at_or_above: 0.80}
```

Per ogni caso il risultato conserva entrambi:

- `jeff_status`: status emesso dalla CLI con il catalogo corrente;
- `eval_status`: status ricalcolato con l'override effettivo.

In assenza di override devono coincidere; la differenza è un errore di configurazione. Gli override sono solo semantica dell'eval, non cambiano Jeff e sono vietati nel profilo real release. La compatibilità della baseline include anche `metrics_version`, fingerprint della configurazione effettiva, timeout/concorrenza e policy dei gate; il binary hash può differire.

Comandi previsti:

```sh
go run ./evals/cmd/jeff-eval validate --dataset evals/datasets/dev/cases.yaml
go run ./evals/cmd/jeff-eval run --dataset evals/datasets/dev/cases.yaml \
  --profile smoke --provider synthetic --out evals/out
```

Il comando `run` continua sugli altri casi e termina:

- `0`: risultati completi e gate superati;
- `1`: risultati prodotti ma regressione/gate fallito;
- `2`: manifest, config, baseline o esecuzione non sufficientemente validi.

`results.json` contiene almeno `schema_version`, metadata run/dataset/catalogo/modello/provider, casi ordinati, output Jeff sanitizzato, target, diagnostiche cross-rule, errori, attempt/latency/usage/costo, metriche globali e per-rule, pair, confronto baseline e gate. `summary.md` contiene tabelle globali/per-rule, pair fallite, activation inattese, inconclusive/errori, latency/costo e regressioni; il workflow la pubblica anche in `$GITHUB_STEP_SUMMARY`.

Il risultato non contiene sorgente, request/response raw o segreti.

## Semantica metriche

L'unità metrica è l'osservazione `(case_id, repetition)`; retry HTTP e attempt del provider non creano nuove osservazioni. Globalmente si riportano metriche micro, per regola e anche il numero di casi unici.

Solo `clean` e `violation` entrano nella confusion matrix:

- `violation` = gold 1; `clean` = gold 0;
- `ambiguous` è riportato separatamente;
- `not_applicable` verifica soltanto il selector e non è un negativo.

Per i casi binari:

```text
precision = TP / (TP + FP)
recall = TP / tutti i gold violation
F0.5 = 1.25 * TP / (1.25 * TP + 0.25 * FN + FP)
false_positive_rate = FP / tutti i gold clean
score_coverage = target con score valido / casi binari
decision_coverage = target pass o violation / casi binari
inconclusive_rate = target inconclusive / casi binari
unavailable_rate = target mancante per error/provider/JSON invalido / casi binari
Brier = somma((noul - gold)^2) / target con score valido
ECE = somma_b (count_b / target con score valido) * |score_medio_b - prevalenza_b|
```

Un denominatore vuoto è `null`, mai `0` o `NaN`. Un `noul` disponibile contribuisce al Brier/ECE anche se è nella fascia inconclusive; un risultato mancante non diventa automaticamente pass. Il report distingue `FN` decisionali, abstain e unavailable, e il recall mantiene nel denominatore tutti i positivi per non gonfiare la qualità. Con casi presenti ma nessuna detection, F0.5 vale `0`; ECE è informativo sotto 100 casi unici e non è gate in v1.

Calibration usa bin fissi `[0,.1) ... [.9,1]`, con count, score medio, prevalenza e ECE, globalmente e per regola.

### Cross-rule e pair

Per ogni caso, ogni regola diversa dal target che risulta `violation` è un'activation. Il report produce conteggi/rate e matrice `target_rule -> activated_rule`. Un'allowlist opzionale per caso/regola mantiene l'activation visibile ma non la fa fallire; non viene chiamata false positive perché non esiste una gold completa per le altre regole.

Per ogni pair e ogni repetition, `flip_success` è:

```text
clean.eval_status == pass && violation.eval_status == violation
```

`flip_rate` usa nel denominatore tutte le coppie attese moltiplicate per le repetition, inclusi lati inconclusive/unavailable; sono riportati anche `pair_coverage`, ordine degli score (`noul_violation > noul_clean`) e delta medio con il proprio denominatore. Le activation allowlisted restano visibili e l'allowlist entra nel dataset hash.

### Latency e costo

Si registrano wall latency del subprocess, provider latency, numero di attempt/retry e percentile deterministico nearest-rank. La latenza non è gate di default.

- `synthetic`: server locale deterministico; target clean = score basso, violation = alto, ambiguous = punto medio della fascia della regola, altre regole = basso; usage fittizio e identificato come sintetico.
- `real`: proxy locale che inoltra solo `/v1/systemone`, non salva Authorization o sorgenti, misura ogni attempt e legge `usage.input_tokens`, `usage.output_tokens` e l'eventuale `usage.cost` senza alterare la risposta.

Il costo calcolato usa pricing configurabile; con il pricing pubblicato attuale è `input_tokens * 0.042 / 1_000_000`, output zero. Se manca usage o pricing, `cost_usd` è `null` con `cost_status` esplicito, mai zero. Si conservano costo riportato dal provider e costo calcolato quando entrambi esistono. Il costo per target rule è il costo della richiesta batch attribuito al caso, quindi non va sommato per ricostruire il totale globale. Non esiste un limite di costo imposto dal runner: il costo è osservabilità, non un motivo per interrompere la suite.

## Baseline e regressioni

Una baseline è un `results.json` approvato e versionato. È compatibile solo con stesso dataset/selection hash, split/versione, modello, catalog hash, effective-threshold hash, provider mode e ripetizioni. Il binary hash può differire. Incompatibilità = exit `2`, mai baseline ignorata.

La config esprime gate globali e per-rule per:

- nuovi false positive e missed violation;
- calo coverage/flip rate;
- aumento inconclusive/unavailable;
- aumento Brier/ECE;
- nuove activation cross-rule non allowlisted;
- opzionalmente delta p95 latency e costo quando comparabili.

Il confronto include anche transizioni case-level (TP diventato pass/inconclusive/unavailable, nuovo FP, pair precedentemente corretta non più corretta). Nessun comando CI aggiorna automaticamente la baseline.

## Slices

### [x] S1 — Schema, loader, validator e hash

- Requirements: R1, R2, R6
- Files: `evals/internal/harness/dataset.go`, `evals/internal/harness/config.go`, `evals/internal/harness/dataset_test.go`, `evals/config.yaml`.
- Outcome: YAML/JSON strict, config strict, catalogo reale, source inline/file, pair, validazione congiunta dei tre split, limiti, hash canonici e diagnostica di validazione.
- Not doing: subprocess o provider.
- Mandatory check: `go test ./evals/internal/harness -run 'Test.*Dataset|Test.*Materialize' -count=1`.
- Runtime/manual check: `NOT APPLICABLE`.
- Result: PASS — 2026-09-19, cwd `/home/alessandro/Work/tries/jeff`, HEAD `263428e` con worktree dirty: `go test ./evals/internal/harness -run 'Test.*Dataset|Test.*Materialize' -count=1`, `go test ./evals/internal/harness -count=1`, `go test ./... -count=1`, `go vet ./...` e `git diff --check` passano. Evidenza: loader strict YAML/JSON, config strict, sorgenti inline/file, materializzazione senza symlink, pair completi, overlap cross-split e hash canonici coperti dai test.

### [x] S2 — Runner end-to-end sintetico

- Requirements: R1, R3, R6
- Files: `evals/cmd/jeff-eval/main.go`, `evals/internal/harness/runner.go`, `evals/internal/harness/synthetic.go`, testdata e `runner_test.go`.
- Outcome: workspace isolato, binario Jeff reale con provenienza registrata, `--no-cache`, child environment controllato, provider sintetico deterministico che verifica il payload osservato, JSON/exit code/errori riconciliati e risultati ordinati.
- Not doing: chiamate reali o baseline.
- Mandatory check: build Jeff + `go test ./evals/internal/harness -run 'Test.*Runner|Test.*Synthetic' -count=1`.
- Runtime/manual check: smoke locale con `--provider synthetic`.
- Result: PASS — 2026-09-19, cwd `/home/alessandro/Work/tries/jeff`, HEAD `263428e` dirty: `go test ./evals/internal/harness -run 'Test.*Runner|Test.*Synthetic' -count=1`, `go test ./... -count=1`, `go vet ./...` e `git diff --check` passano. Smoke manuale senza rete con `go run ./evals/cmd/jeff-eval validate ...` e `run ... --profile smoke --provider synthetic --out ...` ha prodotto `results.json` (1 caso); test end-to-end ha verificato binario Jeff reale, payload, `--no-cache`, exit code, selezione, provenance e assenza di sorgenti/credenziali nei risultati.

### [x] S3 — Metriche, calibration, pair e report

- Requirements: R4
- Files: `evals/internal/harness/metrics.go`, `evals/internal/harness/report.go`, relativi test.
- Outcome: formule globali/per-rule con unità `(case_id, repetition)`, denominatori null, cross-rule, flip, latency percentiles, Brier/calibration, `metrics_version`, `results.json` e `summary.md`.
- Not doing: dati reali o CI.
- Mandatory check: `go test ./evals/internal/harness -run 'Test.*Metric|Test.*Report' -count=1` con osservazioni costruite a mano, non derivate dal runner.
- Runtime/manual check: `NOT APPLICABLE`.
- Result: PASS — 2026-09-19, cwd `/home/alessandro/Work/tries/jeff`, HEAD `263428e` dirty: `go test ./evals/internal/harness -run 'Test.*Metric|Test.*Report' -count=1`, `go test ./evals/internal/harness -count=1`, `go test ./... -count=1`, `go vet ./...` e `git diff --check` passano. Test costruiti a mano verificano confusion matrix, F0.5 senza `0/0`, denominatori `null`, score coverage, Brier/ECE, cross-rule, pair/repetition, nearest-rank latency e summary privo di sorgenti raw.

### [x] S4 — Dataset dev/test completo

- Requirements: R2, R6
- Files: `evals/datasets/dev/cases.yaml`, `evals/datasets/dev/fixtures/`, `evals/datasets/test/cases.yaml`, `evals/datasets/test/fixtures/`, `evals/README.md`.
- Outcome: almeno un minimal pair clean/violation per ciascuna GEN001–GEN007, casi ambiguous/not_applicable motivati, tag smoke e nessun overlap tra tutti gli split; seed release iniziale di 5 pair per regola, senza presentarlo come evidenza statistica definitiva.
- Not doing: fixture holdout, coperto da S7.
- Mandatory check: comando `validate` su entrambi gli split e `go test ./... -count=1`.
- Runtime/manual check: smoke sintetico sul tag `smoke`.
- Result: PASS — 2026-09-19, cwd `/home/alessandro/Work/tries/jeff`, HEAD `263428e` dirty: `go run ./evals/cmd/jeff-eval validate --dataset evals/datasets/dev/cases.yaml --dataset evals/datasets/test/cases.yaml` PASS con suite hash `82ac0f9c4461f5a479d5d1d194432d5cf077d53e063f07bee18b4b28f59f3dd1`; `go test ./... -count=1`, `go vet ./...` e `git diff --check` passano. Smoke synthetic locale sul dev ha eseguito 16 casi e prodotto `results.json`/`summary.md` senza rete.

### [x] S5 — Baseline e confronto regressioni

- Requirements: R5
- Files: `evals/internal/harness/baseline.go` oppure modulo equivalente, test regressioni, `evals/baselines/synthetic-smoke.json`.
- Outcome: baseline synthetic e real separate, compatibile/incompatibile, gate per-rule/global, selezione vuota/null fail-closed, exit `1` per regressione ed exit `2` per incompatibilità o misura non valida.
- Not doing: aggiornamento automatico baseline.
- Mandatory check: test con mutazioni note per ogni direzione di gate + smoke con baseline.
- Runtime/manual check: `NOT APPLICABLE`.
- Result: PASS codice/test, baseline approvata DEFERRED — 2026-09-19, cwd `/home/alessandro/Work/tries/jeff`, HEAD `263428e` dirty: `go test ./evals/internal/harness -run 'Test.*Baseline' -count=1` e `go test ./... -count=1` passano. Sono coperti compatibility metadata, mismatch exit-2, regressioni case/rule/global, threshold gate fail-closed, null metriche e confronto end-to-end con baseline temporanea. Non viene creato `evals/baselines/synthetic-smoke.json`: la baseline versionata richiede il catalogo GEN001–GEN007 in un commit clean, come deciso nel checkpoint.

### [x] S6 — Metering proxy, usage, latency e costo

- Requirements: R4, R6
- Files: `evals/internal/harness/meter.go`, `meter_test.go`, config pricing.
- Outcome: proxy `httptest`/localhost senza retry o redirect propri, equivalenza HTTP con Jeff, forwarding, usage/attempt/retry, costo calcolato/unknown e sanitizzazione senza esporre sorgenti o segreti.
- Not doing: modifica a `internal/typesafe.Response`.
- Mandatory check: `go test ./evals/internal/harness -run 'Test.*Meter|Test.*Cost' -count=1`.
- Runtime/manual check: una run reale con la credenziale locale o la secret CI dedicata; nessun budget artificiale, costo solo misurato.
- Result: PASS — 2026-09-19, cwd `/home/alessandro/Work/tries/jeff`, HEAD `263428e` dirty: test proxy/metering, retry/usage/costo e `go test ./... -count=1`, `go vet ./...`, `git diff --check` passano. Run reale locale autorizzata su `dev --profile smoke --provider real` con chiave caricata dal keyring: 16 casi, 15 attempt provider (il not-applicable non effettua chiamata), 0 unavailable, costo calcolato `$0.00066339`; exit `1` per gate di qualità reale (recall/inconclusive/Brier/flip e 4 activation cross-rule), non per errore di esecuzione. Output: `/tmp/jeff-eval-real-hT20VN/results.json` e `summary.md`.

### [x] S7 — CI PR, release e holdout

- Requirements: R1, R3, R6
- Files: `.github/workflows/evals.yml`, `evals/datasets/holdout/cases.yaml`, `evals/datasets/holdout/fixtures/`, `evals/README.md`.
- Outcome: PR esegue smoke sintetico e pubblica risultati; `workflow_dispatch` protetto sul ref autorizzato esegue test e holdout in job distinti anche se uno fallisce, con la secret CI dedicata; entrambi gli split producono le stesse metriche e breakdown sullo SHA esatto.
- Not doing: chiamate reali con segreti nel job `pull_request`.
- Mandatory check: validazione YAML del workflow, `go test ./... -count=1`, smoke CI-like senza rete.
- Runtime/manual check: workflow release/holdout in environment protetto.
- Result: PASS codice/check CI-like, release runtime DEFERRED — 2026-09-19, cwd `/home/alessandro/Work/tries/jeff`, HEAD `263428e` dirty: aggiunti workflow PR synthetic con artifact e `$GITHUB_STEP_SUMMARY`, workflow_dispatch su `main` con environment `evals-release`, secret dedicata `TYPESAFE_API_KEY_EVALS`, job indipendenti test/holdout e guard del ref; aggiunto holdout congelato con 7 pair GEN001–GEN007. `ruby -e 'require "yaml"; YAML.load_file(".github/workflows/evals.yml")'`, validazione dev/test/holdout, smoke synthetic holdout 14 casi, `go test ./... -count=1`, `go vet ./...` e `git diff --check` passano. Il workflow release non è stato eseguito: richiede environment/secret CI protetti.

## Requirement changes and plan deviations

- 2026-09-19 — old: piano non definiva provenienza, semantica delle repetition, overlap cross-split, sanitizzazione e default operativi; new: default Astra approvati e incorporati nelle sezioni sopra; reason: `discovery`; affected slices: S1–S7 e relativi check.
- 2026-09-19 — old: baseline dirty implicita e holdout descritto come verifica indipendente; new: solo checkout clean per baseline e holdout pubblico definito come secondo split congelato, non blind; reason: `discovery`; affected slices: S1, S5, S7.
- 2026-09-19 — old: threshold override senza slice proprietaria; new: validazione e hash in S1/S3, override vietati nel profilo real release; reason: `discovery`; affected slices: S1, S3, S5.
.

## Last checkpoint

- Revision: `HEAD` (`feat(evals): add versioned evaluation harness`); worktree dirty per modifiche preesistenti al catalogo/documentazione/test, mentre il harness/CI/handoff è committato.
- Checks: S1–S7 codice PASS con test mirati, threshold `jeff_status`/`eval_status`, allowlist activation, usage/costo/gate performance, validazione dev/test/holdout, smoke synthetic dev 16 casi e holdout 14 casi, run reale locale dev 16 casi dal keyring, baseline CLI temporanea, workflow YAML parse, `go test ./... -count=1`, `go vet ./...` e `git diff --check`; review Astra completata.
- Blockers: baseline versionata differita finché il catalogo corrente non è fissato in un commit clean; workflow release/holdout CI richiede environment protetto e secret dedicata.
- Next action: fissare catalogo in commit clean, generare/approvare baseline synthetic e poi lanciare il workflow release su `main`.
