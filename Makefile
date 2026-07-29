GO_APPS := $(patsubst %/go.mod,%,$(wildcard */go.mod))

.PHONY: go-test-build

go-test-build:
	mkdir -p .build/bin
	for app in $(GO_APPS); do go -C "$$app" build -o "../.build/bin/$$app" .; done
