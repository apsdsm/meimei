package catalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDockerfile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Dockerfile")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The shape of the real api Dockerfile: a builder stage and a slim final one.
const twoStage = `
# syntax=docker/dockerfile:1
ARG GO_VERSION=1.25

FROM golang:1.25 AS builder
WORKDIR /src
RUN go build -o /out/api ./cmd/api

FROM alpine:3.21
COPY --from=builder /out/api /usr/local/bin/api
EXPOSE 40102
CMD ["api"]
`

func TestReadDockerfile(t *testing.T) {
	df, err := ReadDockerfile(writeDockerfile(t, twoStage))
	if err != nil {
		t.Fatalf("ReadDockerfile: %v", err)
	}

	if len(df.Stages) != 2 {
		t.Fatalf("got %d stages, want 2: %+v", len(df.Stages), df.Stages)
	}
	if df.Stages[0].Image != "golang:1.25" || df.Stages[0].As != "builder" {
		t.Errorf("first stage = %+v, want golang:1.25 AS builder", df.Stages[0])
	}
	if df.Stages[1].Image != "alpine:3.21" || df.Stages[1].As != "" {
		t.Errorf("second stage = %+v, want an unnamed alpine:3.21", df.Stages[1])
	}
	if len(df.Expose) != 1 || df.Expose[0] != 40102 {
		t.Errorf("Expose = %v, want [40102]", df.Expose)
	}
}

// Base names the language, so it reads the FIRST stage — the last one is a slim
// runtime that says nothing about what the image is.
func TestBase(t *testing.T) {
	cases := []struct {
		image string
		want  string
	}{
		{"golang:1.25", "golang"},
		{"oven/bun:1", "bun"},
		{"node:22-alpine", "node"},
		{"public.ecr.aws/docker/library/golang:1.25", "golang"},
		{"alpine", "alpine"},
		{"golang@sha256:abc123", "golang"},
	}
	for _, tc := range cases {
		df := Dockerfile{Stages: []Stage{{Image: tc.image}}}
		if got := df.Base(); got != tc.want {
			t.Errorf("Base(%q) = %q, want %q", tc.image, got, tc.want)
		}
	}

	if got := (Dockerfile{}).Base(); got != "" {
		t.Errorf("Base of an empty Dockerfile = %q, want empty", got)
	}
}

func TestReadDockerfileSkipsFromFlags(t *testing.T) {
	df, err := ReadDockerfile(writeDockerfile(t,
		"FROM --platform=$BUILDPLATFORM golang:1.25 AS builder\n"))
	if err != nil {
		t.Fatal(err)
	}
	if df.Stages[0].Image != "golang:1.25" {
		t.Errorf("Image = %q, want the flag skipped", df.Stages[0].Image)
	}
	if df.Stages[0].As != "builder" {
		t.Errorf("As = %q, want builder", df.Stages[0].As)
	}
}

func TestReadDockerfileInstructionsAreCaseInsensitive(t *testing.T) {
	df, err := ReadDockerfile(writeDockerfile(t, "from golang:1.25 as builder\nexpose 8080\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(df.Stages) != 1 || df.Stages[0].As != "builder" {
		t.Errorf("stages = %+v, want a lowercase from/as to parse", df.Stages)
	}
	if len(df.Expose) != 1 {
		t.Errorf("Expose = %v, want lowercase expose to parse", df.Expose)
	}
}

func TestReadDockerfileExposeVariants(t *testing.T) {
	df, err := ReadDockerfile(writeDockerfile(t, "FROM alpine\nEXPOSE 8080/tcp 9090\nEXPOSE $PORT\n"))
	if err != nil {
		t.Fatal(err)
	}
	// $PORT is unresolvable without the build args, so it is dropped rather
	// than guessed at.
	want := []int{8080, 9090}
	if len(df.Expose) != len(want) {
		t.Fatalf("Expose = %v, want %v", df.Expose, want)
	}
	for i := range want {
		if df.Expose[i] != want[i] {
			t.Errorf("Expose = %v, want %v", df.Expose, want)
		}
	}
}

func TestReadDockerfileMissing(t *testing.T) {
	_, err := ReadDockerfile(filepath.Join(t.TempDir(), "nope"))
	if err == nil {
		t.Fatal("want an error for a missing file")
	}
}

// A RUN line longer than bufio's default buffer must not truncate the file:
// the stages after it are exactly what we would lose.
func TestReadDockerfileHandlesVeryLongLines(t *testing.T) {
	long := "FROM golang:1.25 AS builder\nRUN echo " + strings.Repeat("x", 100_000) + "\nFROM alpine:3.21\n"
	df, err := ReadDockerfile(writeDockerfile(t, long))
	if err != nil {
		t.Fatalf("ReadDockerfile: %v", err)
	}
	if len(df.Stages) != 2 {
		t.Errorf("got %d stages, want 2 — the line after the long RUN was lost", len(df.Stages))
	}
}
