BINARY  := airwindows
AW      := third_party/airwin2rack/src
VERSION := $(shell sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' hack.json)

CXX      ?= g++
CXXFLAGS ?= -O2
CXXFLAGS += -fPIC -std=c++17 -D_USE_MATH_DEFINES -w -I$(AW) -Idsp

LIB_SRCS := $(wildcard $(AW)/autogen_airwin/*.cpp) \
            $(AW)/AirwinRegistry.cpp \
            $(AW)/airwin_consolidated_base.cpp \
            dsp/awchain.cpp
LIB_OBJS := $(patsubst %.cpp,build/obj/%.o,$(LIB_SRCS))

# Fully static: the binary carries libstdc++ and glibc, so it runs on any
# Push OS release no matter which glibc the build machine has. netgo and
# osusergo keep the static glibc away from DNS and user lookups.
GO_TAGS    := netgo,osusergo
GO_LDFLAGS := -s -w -X main.buildVersion=$(VERSION) -linkmode external -extldflags '-static'

.PHONY: all lib build test vet package deploy clean

all: build

lib: build/libairwin.a

build/libairwin.a: $(LIB_OBJS)
	@rm -f $@
	ar rcs $@ $^

build/obj/%.o: %.cpp
	@mkdir -p $(dir $@)
	@$(CXX) $(CXXFLAGS) -c $< -o $@

build/obj/dsp/awchain.o: dsp/awchain.h

build: lib
	cd src && CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
	  go build -tags $(GO_TAGS) -ldflags "$(GO_LDFLAGS)" -o ../$(BINARY) .

test: lib
	cd src && CGO_ENABLED=1 go test ./...

vet: lib
	cd src && CGO_ENABLED=1 go vet ./...

# The tarball push-catalog installs: its single top-level entry is the
# hack's id (catalog/PUBLISHING.md in ableton-push-hack).
package: build
	@rm -rf build/pkg && mkdir -p build/pkg/$(BINARY)
	cp $(BINARY) hack.json README.md MANUAL.md THIRD_PARTY_LICENSES.md LICENSE build/pkg/$(BINARY)/
	tar -czf $(BINARY).tar.gz -C build/pkg $(BINARY)

# Copy to a Push for a test (ssh as in the Braids repo: ableton@ for files,
# root@ for the service). PUSH=push.local by default.
PUSH ?= push.local
DEST := /data/push-hack/hacks/$(BINARY)
deploy: build
	-ssh root@$(PUSH) "/etc/init.d/push-hack-$(BINARY) stop"
	ssh ableton@$(PUSH) "mkdir -p $(DEST)"
	scp $(BINARY) hack.json ableton@$(PUSH):$(DEST)/
	-ssh root@$(PUSH) "/etc/init.d/push-hack-$(BINARY) start"

clean:
	rm -rf build $(BINARY) $(BINARY).tar.gz
