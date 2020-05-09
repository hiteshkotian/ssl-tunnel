default: build

build: test
	@echo Building server code
	@mkdir -p ./bin
	# @go build ./server
	@go build ./proxy
	@go build ./handler
	@echo Building binary
	@mkdir -p ./bin
	# @go build -o ./bin/secure-tunnel ./cmd/server_main.go
	@go build -o ./bin/proxy ./cmd/proxy_main.go
	# @go build -o ./bin/handler_demo ./cmd/handler_demo.go

test:
	@echo Executing unit tests
	# @go test ./server
	@go test ./proxy

clean:
	@echo Cleaning up binaries
	@rm -rf bin