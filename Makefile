APP=srtrelay
ARGS=

.PHONY: build run test clean

build:
	mkdir -p bin
	go build -o ./bin/$(APP) ./cmd/srtrelay

run:
	go run ./cmd/srtrelay $(ARGS)

test:
	go test ./...

clean:
	rm -rf ./bin
