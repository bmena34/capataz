PREFIX ?= /usr/local
BINARY  = capataz

.PHONY: build install uninstall test vet

build:
	go build -o $(BINARY) .

install: build
	sudo install -d $(PREFIX)/bin
	sudo install -m 755 $(BINARY) $(PREFIX)/bin/$(BINARY)

uninstall:
	sudo rm -f $(PREFIX)/bin/$(BINARY)

test:
	go test ./...

vet:
	go vet ./...
