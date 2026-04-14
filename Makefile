.PHONY: run test migrate

run:
	go run .

test:
	go test ./...

migrate:
	./scripts/migrate.sh
