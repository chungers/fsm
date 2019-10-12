# Set an output prefix, which is the local directory if not specified
PREFIX?=$(shell pwd -L)

# Used to populate version variable in main package.
VERSION?=$(shell git describe --match 'v[0-9]*' --dirty='.m' --always)
REVISION?=$(shell git rev-list -1 HEAD)
GO_BUILD_TAGS?=$()

PKGS_AND_MOCKS := $(shell go list ./... | grep -v /vendor)
PKGS := $(shell echo $(PKGS_AND_MOCKS) | tr ' ' '\n' | grep -v /mock$)
PKGS_TEST := $(shell echo $(PKGS_AND_MOCKS) | tr ' ' '\n' | grep pkg$)

# Allow turning off function inlining and variable registerization
ifeq (${DISABLE_OPTIMIZATION},true)
	GO_GCFLAGS=-gcflags "-N -l"
	VERSION:="$(VERSION)-noopt"
endif

.PHONY: clean vet lint
.DEFAULT: all
all: clean fmt vet lint test _examples/simple

AUTHORS: .mailmap .git/HEAD
	git log --format='%aN <%aE>' | sort -fu > $@

fmt:
	@echo "+ $@"
	@test -z "$$(gofmt -s -l . 2>&1 | grep -v ^vendor/ | tee /dev/stderr)" || \
		(echo >&2 "+ please format Go code with 'gofmt -s', or use 'make fmt-save'" && false)

fmt-save:
	@echo "+ $@"
	@gofmt -s -l . 2>&1 | grep -v ^vendor/ | xargs gofmt -s -l -w

vet:
	@echo "+ $@"
	@go vet $(PKGS)

lint:
	@echo "+ $@"
	$(if $(shell which golint || echo ''), , \
		$(shell go get -u golang.org/x/lint/golint))
	@test -z "$$($(shell go list -f {{.Target}} golang.org/x/lint/golint) ./... 2>&1 | grep -v ^vendor/ | grep -v mock/ | tee /dev/stderr )"

test:
	@echo "+ $@"
	@go test -timeout 30s -race -count=1 -v $(PKGS_TEST)

clean:
	@echo "+ $@"
	@rm -rf build
	@mkdir -p build
	@rm -rf _examples/simple

define binary_target_template
$(1): $(1).go lint vet test
	@$(eval HASH := $(shell git hash-object $(1).go))
	@echo "+ building $(1) hash=$(HASH)"
ifneq (,$(findstring .m,$(VERSION)))
		@echo "\nWARNING - repository contains uncommitted changes, tagged binaries as dirty\n"
endif
	go build -o $(1) \
		-tags "$(GO_BUILD_TAGS)" \
		-ldflags "\
		-X main.VERSION=$(VERSION) \
		-X main.REVISION=$(REVISION) \
		-X main.HASH=$(HASH)\
		" \
		$(1).go
endef

define define_binary_target
	$(eval $(call binary_target_template,$(1)))
endef

# All the possible targets

# Simple is the example that shows how to use the fsm in a server that
# is able to watch over a set of other http servers and start them
# if necessary.
$(call define_binary_target,_examples/simple)


