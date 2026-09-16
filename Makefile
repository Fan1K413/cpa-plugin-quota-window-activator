PLUGIN := quota-window-activator
DIST := dist
UNAME_S := $(shell uname -s)

ifeq ($(OS),Windows_NT)
EXT := dll
else ifeq ($(UNAME_S),Darwin)
EXT := dylib
else
EXT := so
endif

.PHONY: test build clean

test:
	go test ./...

build: test
	mkdir -p $(DIST)
	CGO_ENABLED=1 go build -buildvcs=false -trimpath -buildmode=c-shared -o $(DIST)/$(PLUGIN).$(EXT) ./cmd/plugin
	rm -f $(DIST)/$(PLUGIN).h

clean:
	rm -rf $(DIST)
