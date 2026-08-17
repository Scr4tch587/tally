.PHONY: run test migrate bench bench-load k8s-up k8s-down

KIND_CLUSTER ?= tally

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

k8s-up:
	kind get clusters | grep -qx $(KIND_CLUSTER) || kind create cluster --name $(KIND_CLUSTER)
	docker build -t tally:local .
	kind load docker-image tally:local --name $(KIND_CLUSTER)
	kubectl apply -f deploy/k8s/namespace.yaml
	kubectl -n tally create configmap tally-migrations --from-file=migrations/ --dry-run=client -o yaml | kubectl apply -f -
	kubectl -n tally create configmap tally-migrate-script --from-file=scripts/migrate.sh --dry-run=client -o yaml | kubectl apply -f -
	kubectl -n tally delete job tally-migrate --ignore-not-found
	kubectl apply -f deploy/k8s/
	kubectl -n tally rollout status statefulset/postgres --timeout=180s
	kubectl -n tally wait --for=condition=complete job/tally-migrate --timeout=180s
	kubectl -n tally rollout status deployment/tally --timeout=180s

k8s-down:
	kind delete cluster --name $(KIND_CLUSTER)

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
