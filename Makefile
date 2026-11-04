BINARY  := terraform-provider-truenas-installer
VERSION ?= 0.1.0
DEV_DIR ?= $(HOME)/.terraform.d/plugins

.PHONY: default build install test vet fmt clean

default: build

build:
	CGO_ENABLED=0 go build -o $(BINARY) .

install: build
	mkdir -p $(DEV_DIR)/$(BINARY)
	cp $(BINARY) $(DEV_DIR)/$(BINARY)/

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -f $(BINARY)
