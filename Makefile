.PHONY: test test-race vet bench bench-matching bench-storage bench-batch bench-journal bench-count profile-cpu profile-heap check build proto-gen proto-check

build:
	go build -o build/cosmos-orderbookd ./cmd/cosmos-orderbookd
	go build -o build/orderbook-sequencer ./cmd/orderbook-sequencer

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

# Keeper execution benchmarks are not CometBFT block throughput.
BENCH = -benchmem -count=1 -run=^$$

bench:
	go test $(BENCH) -bench=. ./benchmarks/ ./x/exchange/keeper/ ./x/batch/keeper/ ./sequencer/

bench-matching:
	go test $(BENCH) -bench='Benchmark(SingleFill|Makers|MultiLevel|PartialFill|NonCrossing|DeepBook|SamePrice)' ./benchmarks/

bench-storage:
	go test $(BENCH) -bench=BenchmarkStorage ./x/exchange/keeper/

bench-batch:
	go test $(BENCH) -bench=BenchmarkBatch ./x/batch/keeper/

bench-journal:
	go test $(BENCH) -bench=BenchmarkJournal ./sequencer/

# stdout is benchstat input, for example: make bench-count >matching.txt && benchstat matching.txt
bench-count:
	go test -benchmem -count=6 -run=^$$ -bench=. ./benchmarks/ ./x/exchange/keeper/ ./x/batch/keeper/ ./sequencer/

profile-cpu:
	go test -bench=BenchmarkMakers1000 -benchmem -count=1 -run=^$$ -cpuprofile=cpu.out ./benchmarks/

profile-heap:
	go test -bench=BenchmarkMakers1000 -benchmem -count=1 -run=^$$ -memprofile=mem.out ./benchmarks/

check: test test-race vet

proto-gen:
	./scripts/proto-gen.sh

proto-check:
	./scripts/proto-gen.sh --check
