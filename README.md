# jeff

CLI Go per eseguire controlli basati su [Jev](https://docs.typesafe.ai/introduction) sul codice.

Il progetto è ancora allo scheletro iniziale: l'implementazione delle regole arriverà in seguito.

## Requisiti

- Go 1.25+

## Sviluppo

Avvio locale:

```sh
go run ./cmd/jeff
```

Verifiche:

```sh
go fmt ./...
go vet ./...
go test ./...
```

Build:

```sh
go build -o bin/jeff ./cmd/jeff
```

## Repository remoto

Dopo aver creato il repository online:

```sh
git remote add origin <url-del-repository>
git push -u origin main
```
