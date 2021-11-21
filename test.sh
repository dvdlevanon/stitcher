#!/bin/bash

mkdir -p out

go generate
go test -coverprofile=./out/cover.out -failfast -cover ./src/... $@ || exit 1
go tool cover -func out/cover.out || exit 1
go tool cover -html out/cover.out -o out/coverage.html