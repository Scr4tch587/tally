.PHONY: run test migrate bench bench-load

PAIRS ?= 1000
WORKERS ?= 16
SEED ?= 42
ARRIVAL ?= paired
DECOY_RATIO ?= 0.4
BASE_URL ?= http://localhost:8080
DATABASE_URL ?= postgres://tally:tally@localhost:5432/tally
OUTPUT ?= bench-results/latest.json
STEPS ?= 100,1000,5000,10000,25000
RESUME_P99_MS ?= 250

run:
	go run .

test:
	go test ./...

migrate:
	./scripts/migrate.sh

bench:
	go run ./cmd/bench \
		-mode correctness \
		-pairs $(PAIRS) \
		-workers $(WORKERS) \
		-seed $(SEED) \
		-arrival $(ARRIVAL) \
		-decoy-ratio $(DECOY_RATIO) \
		-base-url $(BASE_URL) \
		-database-url $(DATABASE_URL) \
		-output $(OUTPUT)

bench-load:
	go run ./cmd/bench \
		-mode load \
		-workers $(WORKERS) \
		-seed $(SEED) \
		-arrival $(ARRIVAL) \
		-decoy-ratio $(DECOY_RATIO) \
		-base-url $(BASE_URL) \
		-database-url $(DATABASE_URL) \
		-output $(OUTPUT) \
		-steps $(STEPS) \
		-resume-p99-ms $(RESUME_P99_MS)
