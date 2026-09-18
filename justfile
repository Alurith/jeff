alias cleanup := clean

default:
    @just --list

run *args:
    go run ./cmd/jeff {{args}}

build:
    mkdir -p bin
    go build -o bin/jeff ./cmd/jeff

test:
    go test ./...

check:
    go fmt ./...
    go vet ./...
    go test ./...

clean:
    rm -rf -- bin .jeff-cache
