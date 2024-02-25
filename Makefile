default: help

VERSION?=dev

.PHONY: help
help: # Show help for each of the Makefile recipes.
	@grep -E '^[a-zA-Z0-9 -]+:.*#'  Makefile | sort | while read -r l; do printf "\033[1;32m$$(echo $$l | cut -f 1 -d':')\033[00m:$$(echo $$l | cut -f 2- -d'#')\n"; done

build: # Build the binary. Use VERSION (make build VERSION=1.2.3) to set the build version.
	go build -o paperboy \
  	-ldflags "-X main.version=${VERSION}" \
		main.go

