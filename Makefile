SHELL := /bin/sh

GO ?= go
CC ?= cc

COMPONENTS := apiserver scheduler kubelet runtime cni kube-proxy

.PHONY: all build build-go build-c test test-go test-c fmt clean

all: build

build: build-go build-c

build-go:
	@if find . -type f -name '*.go' -not -path './.git/*' | grep -q .; then \
		echo '[build] Go packages'; \
		$(GO) build ./...; \
	else \
		echo '[build] no Go packages yet'; \
	fi

build-c:
	@if find . -type f \( -name '*.c' -o -name '*.h' \) -not -path './.git/*' | grep -q .; then \
		echo '[build] C sources'; \
		$(MAKE) -C runtime build; \
	else \
		echo '[build] no C sources yet'; \
	fi

test: test-go test-c
	@echo '[test] contract tests are ready for component implementations'

test-go:
	@if find . -type f -name '*_test.go' -not -path './.git/*' | grep -q .; then \
		echo '[test] Go tests'; \
		$(GO) test ./...; \
	else \
		echo '[test] no Go tests yet'; \
	fi

test-c:
	@if test -f runtime/Makefile; then \
		echo '[test] C tests'; \
		$(MAKE) -C runtime test; \
	else \
		echo '[test] no C tests yet'; \
	fi

test-%:
	@if test -d test/contract/$*; then \
		echo '[test-$*] component contract tests'; \
		$(GO) test ./test/contract/$*; \
	elif find . -type f -name '*$*_test.go' -not -path './.git/*' | grep -q .; then \
		echo '[test-$*] matching Go tests'; \
		$(GO) test ./...; \
	else \
		echo '[test-$*] no tests yet for $*'; \
	fi

fmt:
	@if find . -type f -name '*.go' -not -path './.git/*' | grep -q .; then \
		find . -type f -name '*.go' -not -path './.git/*' -exec $(GO)fmt -w {} \;; \
	else \
		echo '[fmt] no Go files yet'; \
	fi
	@if test -f runtime/Makefile; then \
		$(MAKE) -C runtime fmt; \
	else \
		echo '[fmt] no C sources yet'; \
	fi

clean:
	@rm -rf bin build
	@if test -f runtime/Makefile; then $(MAKE) -C runtime clean; fi
