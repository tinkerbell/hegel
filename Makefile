binary := hegel
all: ${binary}
.PHONY: ${binary} test
${binary}: grpc/hegel/hegel.pb.go
${binary}:
	CGO_ENABLED=0 GOOS=$$GOOS go build -ldflags="-X main.GitRev=$(shell git rev-parse --short HEAD)" -o $@ ./$(@D)
ifeq ($(CI),drone)
run: ${binary}
	${binary}
test:
	go test -race -coverprofile=coverage.txt -covermode=atomic ${TEST_ARGS} ./...
else
run: ${binary}
	docker-compose up --build server
test:
	docker-compose up test
endif