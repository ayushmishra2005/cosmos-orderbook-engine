.PHONY: test test-race vet bench check

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

bench:
	go test -bench=. -benchmem -count=1 -run=^$$ ./benchmarks/

check: test test-race vet
