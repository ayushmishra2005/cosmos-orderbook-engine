.PHONY: test test-race vet bench check build

build:
	go build -o build/cosmos-orderbookd ./cmd/cosmos-orderbookd

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

bench:
	go test -bench=. -benchmem -count=1 -run=^$$ ./benchmarks/

check: test test-race vet
