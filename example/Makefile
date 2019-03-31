# Set an output prefix, which is the local directory if not specified
PREFIX?=$(shell pwd -L)

# Used to populate version variable in main package.
VERSION?=$(shell git describe --match 'v[0-9]*' --dirty='.m' --always)
REVISION?=$(shell git rev-list -1 HEAD)

# Allow turning off function inlining and variable registerization
ifeq (${DISABLE_OPTIMIZATION},true)
	GO_GCFLAGS=-gcflags "-N -l"
	VERSION:="$(VERSION)-noopt"
endif

.PHONY: clean vet lint
.DEFAULT: simple
all: clean vet lint simple

AUTHORS: .mailmap .git/HEAD
	git log --format='%aN <%aE>' | sort -fu > $@

vet:
	@echo "+ $@"
	@go vet $(PKGS)

lint:
	@echo "+ $@"
	$(if $(shell which golint || echo ''), , \
		$(error Please install golint: `make get-tools`))
	@test -z "$$(golint ./... 2>&1 | grep -v ^vendor/ | grep -v mock/ | tee /dev/stderr)"

clean:
	@echo "+ $@"
	@rm -rf build
	@mkdir -p build

define binary_target_template
$(1): $(1).go lint vet
	@$(eval HASH := $(shell git hash-object $(1).go))
	@echo "+ build/$(1) hash=$(HASH)"
	@mkdir -p build
ifneq (,$(findstring .m,$(VERSION)))
		@echo "\nWARNING - repository contains uncommitted changes, tagged binaries as dirty\n"
endif
	go build -o build/$(1) \
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

# Sentry is the example that shows how to use the fsm in a server that
# is able to watch over a set of other http servers and start them
# if necessary.
$(call define_binary_target,simple)
