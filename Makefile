.PHONY: build test race vet fmt run benchmark server clean tidy all

BIN := bin
all: build

build: ## build CLI and server binaries
	go build -o $(BIN)/neuroforge ./cmd/neuroforge
	go build -o $(BIN)/neuroforge-server ./cmd/neuroforge-server

test: ## run the full test suite with the race detector
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

run: build ## run the flagship warehouse workload
	./$(BIN)/neuroforge run examples/warehouse-intelligence/workload.yaml --cache --concurrency 4

benchmark: build ## run the benchmark suite
	./$(BIN)/neuroforge benchmark

server: build ## start the control-plane API on :8080
	./$(BIN)/neuroforge-server

doctor: build
	./$(BIN)/neuroforge doctor

tidy:
	go mod tidy

clean:
	rm -rf $(BIN) .neuroforge
