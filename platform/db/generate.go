// Package db holds glossa-server's SQL: migrations/ (the schema, embedded
// into the binary) and queries/ (the input to sqlc). Generated Go lives
// with each bounded context; run `go generate ./db/...` from platform/.
package db

//go:generate go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.31.1 generate -f ../sqlc.yaml
