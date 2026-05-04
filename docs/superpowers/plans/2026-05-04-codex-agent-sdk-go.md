# codex-agent-sdk-go Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a Go port of `@openai/codex-sdk` (TypeScript) at
`/root/codex-agent-sdk-go`, fully aligned with the TS SDK on wire and API,
ready for ccbroker to consume as a codex driver.

**Architecture:** Thin subprocess wrapper. `Codex.StartThread()` /
`ResumeThread()` returns a `Thread`; `Thread.Run` / `RunStreamed` spawn
`codex exec --experimental-json [...]`, write the prompt to stdin, parse
JSONL events from stdout into typed Go events. Pure functions (config
flattening, arg building, env composition, JSON parsing) are split into
files testable without subprocess; subprocess driver wraps them and is
exercised via bash-script fakes.

**Tech Stack:** Go 1.22, stdlib only (`os/exec`, `encoding/json`,
`bufio`, `sync`). No external deps.

**Spec:** `docs/superpowers/specs/2026-05-04-codex-agent-sdk-go-design.md`
in agentserver repo (read this first).

**Working directory:** All tasks operate in `/root/codex-agent-sdk-go`
(new git repo, initialized in Task 1). Plan tasks assume `cd /root/codex-agent-sdk-go`
unless otherwise noted.

**Module path:** `github.com/agentserver/codex-agent-sdk-go`

---

## File Structure

| File | Responsibility |
|---|---|
| `go.mod` | Module declaration, Go 1.22, no deps |
| `README.md` | Quickstart + API summary |
| `.gitignore` | Build artifacts, test binaries |
| `error.go` | `SpawnError`, `NonZeroExitError`, `ParseEventError`, `TurnFailedError` |
| `options.go` | Enums (`SandboxMode`, `ApprovalMode`, `ReasoningEffort`, `WebSearchMode`), `ThreadOptions`, `TurnOptions` data structs |
| `input.go` | `Input` interface, `StringInput`, `PartsInput`, `UserInput`, `joinTextParts` |
| `config.go` | `tomlValue`, `formatTomlKey`, `serializeConfigOverrides`, `flattenConfigOverrides` |
| `events.go` | `ThreadEvent` interface + variants, `parseEvent` |
| `items.go` | `ThreadItem` interface + variants, item unmarshaller |
| `schema.go` | `prepareOutputSchema` (temp file write + cleanup) |
| `exec.go` | `buildArgs`, `composeEnv`, `runExec` (subprocess driver) |
| `codex.go` | `Codex`, `CodexOptions`, `New`, `StartThread`, `ResumeThread` |
| `thread.go` | `Thread`, `Turn`, `StreamedTurn`, `Run`, `RunStreamed`, atomic id capture |
| `examples/quickstart/main.go` | Runnable quickstart example |
| `testdata/fake_codex/` | Bash-script fake codex binaries used by exec tests |
| `*_test.go` | One test file per source file |
| `wire_parity_test.go` | Records argv/stdin/env from the SDK and asserts canonical traces (vs the spec) |

---

## Task 1: Repo bootstrap

**Files:**
- Create: `/root/codex-agent-sdk-go/go.mod`
- Create: `/root/codex-agent-sdk-go/.gitignore`
- Create: `/root/codex-agent-sdk-go/README.md`
- Create: `/root/codex-agent-sdk-go/doc.go`

- [ ] **Step 1: Create the directory and initialize git**

```bash
mkdir -p /root/codex-agent-sdk-go
cd /root/codex-agent-sdk-go
git init
```

- [ ] **Step 2: Write go.mod**

`go.mod`:
```
module github.com/agentserver/codex-agent-sdk-go

go 1.22
```

- [ ] **Step 3: Write .gitignore**

`.gitignore`:
```
# Compiled binaries
*.exe
*.test
*.out
*.prof

# Build / coverage
coverage.out
coverage.html

# Editor / OS
.DS_Store
.idea/
.vscode/

# Examples binaries (built ad-hoc)
examples/*/quickstart
```

- [ ] **Step 4: Write doc.go (package doc)**

`doc.go`:
```go
// Package codex is a Go SDK for the codex CLI binary, a Go port of
// @openai/codex-sdk (TypeScript). It spawns `codex exec --experimental-json`
// per turn, writes the prompt to stdin, and yields typed events parsed
// from stdout JSONL.
//
// See README.md for a quickstart and the design spec at
// docs/superpowers/specs/2026-05-04-codex-agent-sdk-go-design.md (in the
// agentserver repo) for full alignment notes vs the TS SDK.
package codex
```

- [ ] **Step 5: Write README.md skeleton**

`README.md`:
```markdown
# codex-agent-sdk-go

Go SDK for the [codex](https://github.com/openai/codex) CLI, ported from
`@openai/codex-sdk` (TypeScript).

Wraps `codex exec --experimental-json`: spawns the CLI per turn, writes
the prompt to stdin, parses JSONL events from stdout into typed Go events.

## Status

Pre-1.0. API tracks the TS SDK exactly; see the design spec for the
complete alignment list.

## Requirements

- Go 1.22+
- `codex` CLI binary on PATH (or pass `CodexOptions.BinaryPath`)
- Tested against codex-cli >= 0.125.0

## Quickstart

```go
package main

import (
    "context"
    "fmt"
    "os"

    "github.com/agentserver/codex-agent-sdk-go"
)

func main() {
    c := codex.New(codex.CodexOptions{
        APIKey: os.Getenv("OPENAI_API_KEY"),
    })
    t := c.StartThread(codex.ThreadOptions{
        SandboxMode:      codex.SandboxWorkspaceWrite,
        WorkingDirectory: "/tmp/work",
        SkipGitRepoCheck: true,
    })
    turn, err := t.Run(context.Background(),
        codex.StringInput("List the files in this directory"),
        codex.TurnOptions{})
    if err != nil { panic(err) }
    fmt.Println(turn.FinalResponse)
    fmt.Println("thread id:", t.ID())
}
```
```

- [ ] **Step 6: Verify `go build ./...` succeeds**

Run: `cd /root/codex-agent-sdk-go && go build ./...`
Expected: no output, exit 0.

- [ ] **Step 7: Commit**

```bash
cd /root/codex-agent-sdk-go
git add go.mod .gitignore README.md doc.go
git commit -m "chore: bootstrap codex-agent-sdk-go module"
```

---

## Task 2: Error types

**Files:**
- Create: `error.go`
- Create: `error_test.go`

- [ ] **Step 1: Write the failing test**

`error_test.go`:
```go
package codex

import (
	"errors"
	"strings"
	"testing"
)

func TestSpawnError(t *testing.T) {
	inner := errors.New("no such file")
	e := &SpawnError{Err: inner}
	if !strings.Contains(e.Error(), "no such file") {
		t.Errorf("Error() = %q, want it to contain %q", e.Error(), "no such file")
	}
	if !errors.Is(e, inner) {
		t.Errorf("errors.Is should unwrap to inner")
	}
}

func TestNonZeroExitError(t *testing.T) {
	e := &NonZeroExitError{Code: 2, Signal: "", Stderr: "boom"}
	got := e.Error()
	for _, want := range []string{"code 2", "boom"} {
		if !strings.Contains(got, want) {
			t.Errorf("Error() = %q, missing %q", got, want)
		}
	}

	e2 := &NonZeroExitError{Code: -1, Signal: "killed", Stderr: ""}
	if !strings.Contains(e2.Error(), "signal killed") {
		t.Errorf("Error() with signal = %q", e2.Error())
	}
}

func TestParseEventError(t *testing.T) {
	inner := errors.New("unexpected token")
	e := &ParseEventError{Line: "{garbage", Err: inner}
	if !strings.Contains(e.Error(), "unexpected token") {
		t.Errorf("Error() = %q", e.Error())
	}
	if !errors.Is(e, inner) {
		t.Errorf("errors.Is should unwrap")
	}
}

func TestTurnFailedError(t *testing.T) {
	e := &TurnFailedError{Message: "model rejected"}
	if e.Error() != "codex turn failed: model rejected" {
		t.Errorf("Error() = %q", e.Error())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/codex-agent-sdk-go && go test ./... -run 'Error$'`
Expected: FAIL with "undefined: SpawnError" etc.

- [ ] **Step 3: Implement error.go**

`error.go`:
```go
package codex

import "fmt"

// SpawnError wraps a failure from os/exec before the codex process starts
// (e.g., binary not on PATH, permission denied).
type SpawnError struct{ Err error }

func (e *SpawnError) Error() string { return "codex spawn: " + e.Err.Error() }
func (e *SpawnError) Unwrap() error { return e.Err }

// NonZeroExitError is returned when codex exits with a non-zero code or is
// killed by a signal. Stderr is the bounded tail of the subprocess's
// stderr stream (capped at 64KB).
type NonZeroExitError struct {
	Code   int
	Signal string
	Stderr string
}

func (e *NonZeroExitError) Error() string {
	if e.Signal != "" {
		return fmt.Sprintf("codex exited with signal %s: %s", e.Signal, e.Stderr)
	}
	return fmt.Sprintf("codex exited with code %d: %s", e.Code, e.Stderr)
}

// ParseEventError indicates a JSONL line from codex stdout could not be
// parsed into a known event type. ParseEventError does NOT terminate the
// stream; it is wrapped into a synthetic ThreadErrorEvent.
type ParseEventError struct {
	Line string
	Err  error
}

func (e *ParseEventError) Error() string {
	return fmt.Sprintf("parse event: %v (line: %q)", e.Err, truncate(e.Line, 120))
}
func (e *ParseEventError) Unwrap() error { return e.Err }

// TurnFailedError is returned by Thread.Run when codex emits a turn.failed
// event. Thread.RunStreamed does not return this — it yields the
// TurnFailedEvent on the channel and lets the caller decide.
type TurnFailedError struct{ Message string }

func (e *TurnFailedError) Error() string { return "codex turn failed: " + e.Message }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/codex-agent-sdk-go && go test ./... -run 'Error$' -v`
Expected: PASS for all 4 tests.

- [ ] **Step 5: Commit**

```bash
git add error.go error_test.go
git commit -m "feat: error types (SpawnError, NonZeroExitError, ParseEventError, TurnFailedError)"
```

---

## Task 3: Option enums and structs

**Files:**
- Create: `options.go`
- Create: `options_test.go`

- [ ] **Step 1: Write the failing test**

`options_test.go`:
```go
package codex

import "testing"

func TestSandboxModeConstants(t *testing.T) {
	cases := map[SandboxMode]string{
		SandboxReadOnly:         "read-only",
		SandboxWorkspaceWrite:   "workspace-write",
		SandboxDangerFullAccess: "danger-full-access",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
}

func TestApprovalModeConstants(t *testing.T) {
	cases := map[ApprovalMode]string{
		ApprovalNever:     "never",
		ApprovalOnRequest: "on-request",
		ApprovalOnFailure: "on-failure",
		ApprovalUntrusted: "untrusted",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
}

func TestReasoningEffortConstants(t *testing.T) {
	cases := map[ReasoningEffort]string{
		ReasoningMinimal: "minimal",
		ReasoningLow:     "low",
		ReasoningMedium:  "medium",
		ReasoningHigh:    "high",
		ReasoningXHigh:   "xhigh",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
}

func TestWebSearchModeConstants(t *testing.T) {
	cases := map[WebSearchMode]string{
		WebSearchDisabled: "disabled",
		WebSearchCached:   "cached",
		WebSearchLive:     "live",
	}
	for got, want := range cases {
		if string(got) != want {
			t.Errorf("got %q want %q", got, want)
		}
	}
}

func TestThreadOptionsZeroValue(t *testing.T) {
	var o ThreadOptions
	if o.NetworkAccessEnabled != nil { t.Error("NetworkAccessEnabled should be nil zero") }
	if o.WebSearchEnabled != nil     { t.Error("WebSearchEnabled should be nil zero") }
	if o.SkipGitRepoCheck            { t.Error("SkipGitRepoCheck should be false zero") }
	if o.AdditionalDirs != nil       { t.Error("AdditionalDirs should be nil zero") }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'Options|ModeConstants|EffortConstants'`
Expected: FAIL with undefined identifiers.

- [ ] **Step 3: Implement options.go**

`options.go`:
```go
package codex

// SandboxMode mirrors TS `SandboxMode`: codex --sandbox argument.
type SandboxMode string

const (
	SandboxReadOnly         SandboxMode = "read-only"
	SandboxWorkspaceWrite   SandboxMode = "workspace-write"
	SandboxDangerFullAccess SandboxMode = "danger-full-access"
)

// ApprovalMode mirrors TS `ApprovalMode`: codex approval_policy config.
type ApprovalMode string

const (
	ApprovalNever     ApprovalMode = "never"
	ApprovalOnRequest ApprovalMode = "on-request"
	ApprovalOnFailure ApprovalMode = "on-failure"
	ApprovalUntrusted ApprovalMode = "untrusted"
)

// ReasoningEffort mirrors TS `ModelReasoningEffort`.
type ReasoningEffort string

const (
	ReasoningMinimal ReasoningEffort = "minimal"
	ReasoningLow     ReasoningEffort = "low"
	ReasoningMedium  ReasoningEffort = "medium"
	ReasoningHigh    ReasoningEffort = "high"
	ReasoningXHigh   ReasoningEffort = "xhigh"
)

// WebSearchMode mirrors TS `WebSearchMode`.
type WebSearchMode string

const (
	WebSearchDisabled WebSearchMode = "disabled"
	WebSearchCached   WebSearchMode = "cached"
	WebSearchLive     WebSearchMode = "live"
)

// ThreadOptions mirrors TS `ThreadOptions`. See spec §"Public API" for
// the full TS-to-Go field mapping.
type ThreadOptions struct {
	Model                string
	SandboxMode          SandboxMode
	WorkingDirectory     string
	AdditionalDirs       []string
	SkipGitRepoCheck     bool
	ModelReasoningEffort ReasoningEffort
	NetworkAccessEnabled *bool
	WebSearchMode        WebSearchMode
	WebSearchEnabled     *bool
	ApprovalPolicy       ApprovalMode
}

// TurnOptions mirrors TS `TurnOptions`. The TS `signal: AbortSignal` field
// is replaced by the `ctx context.Context` parameter on Run / RunStreamed.
type TurnOptions struct {
	// OutputSchema is an arbitrary JSON-serializable Go value (typically a
	// map[string]any holding a JSON Schema). When non-nil, the SDK marshals
	// it to a temp file and passes --output-schema. The temp file is
	// cleaned up when the turn ends.
	OutputSchema any
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run 'Options|ModeConstants|EffortConstants' -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add options.go options_test.go
git commit -m "feat: option enums (SandboxMode/ApprovalMode/ReasoningEffort/WebSearchMode) + ThreadOptions, TurnOptions"
```

---

## Task 4: Input types + joinTextParts

**Files:**
- Create: `input.go`
- Create: `input_test.go`

- [ ] **Step 1: Write the failing test**

`input_test.go`:
```go
package codex

import (
	"reflect"
	"testing"
)

func TestJoinTextParts_String(t *testing.T) {
	prompt, images := joinTextParts(StringInput("hello"))
	if prompt != "hello" {
		t.Errorf("prompt = %q, want %q", prompt, "hello")
	}
	if images != nil {
		t.Errorf("images = %v, want nil", images)
	}
}

func TestJoinTextParts_Parts_TextOnly(t *testing.T) {
	prompt, images := joinTextParts(PartsInput{
		{Type: InputText, Text: "first"},
		{Type: InputText, Text: "second"},
	})
	if prompt != "first\n\nsecond" {
		t.Errorf("prompt = %q", prompt)
	}
	if images != nil {
		t.Errorf("images = %v", images)
	}
}

func TestJoinTextParts_Parts_Mixed(t *testing.T) {
	prompt, images := joinTextParts(PartsInput{
		{Type: InputText, Text: "describe"},
		{Type: InputLocalImage, Path: "/a.png"},
		{Type: InputText, Text: "and this"},
		{Type: InputLocalImage, Path: "/b.png"},
	})
	if prompt != "describe\n\nand this" {
		t.Errorf("prompt = %q", prompt)
	}
	if !reflect.DeepEqual(images, []string{"/a.png", "/b.png"}) {
		t.Errorf("images = %v", images)
	}
}

func TestJoinTextParts_EmptyParts(t *testing.T) {
	prompt, images := joinTextParts(PartsInput{})
	if prompt != "" {
		t.Errorf("prompt = %q", prompt)
	}
	if images != nil {
		t.Errorf("images = %v", images)
	}
}

func TestInput_SealedInterface(t *testing.T) {
	// Compile-time check: only StringInput and PartsInput satisfy Input.
	var _ Input = StringInput("")
	var _ Input = PartsInput(nil)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'JoinTextParts|Input_'`
Expected: FAIL undefined types.

- [ ] **Step 3: Implement input.go**

`input.go`:
```go
package codex

import "strings"

// Input is the prompt parameter to Run / RunStreamed. Mirrors TS
// `Input = string | UserInput[]`. Use StringInput or PartsInput.
type Input interface{ codexInput() }

// StringInput is a bare-text prompt. Equivalent to
// PartsInput{{Type: InputText, Text: string(s)}}.
type StringInput string

func (StringInput) codexInput() {}

// PartsInput is a sequence of typed input parts. Mirrors TS `UserInput[]`.
type PartsInput []UserInput

func (PartsInput) codexInput() {}

// UserInput mirrors TS:
//   {type: "text", text: string} | {type: "local_image", path: string}
// Set Text when Type==InputText; set Path when Type==InputLocalImage.
type UserInput struct {
	Type UserInputType
	Text string
	Path string
}

type UserInputType string

const (
	InputText       UserInputType = "text"
	InputLocalImage UserInputType = "local_image"
)

// joinTextParts mirrors TS `normalizeInput` (thread.ts:140-156): texts are
// joined with "\n\n", images are extracted to a parallel slice for
// --image flags.
func joinTextParts(input Input) (prompt string, images []string) {
	switch v := input.(type) {
	case StringInput:
		return string(v), nil
	case PartsInput:
		var texts []string
		for _, p := range v {
			switch p.Type {
			case InputText:
				texts = append(texts, p.Text)
			case InputLocalImage:
				images = append(images, p.Path)
			}
		}
		return strings.Join(texts, "\n\n"), images
	}
	return "", nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run 'JoinTextParts|Input_' -v`
Expected: all 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add input.go input_test.go
git commit -m "feat: Input types (StringInput, PartsInput, UserInput) + joinTextParts"
```

---

## Task 5: TOML value formatting

**Files:**
- Create: `config.go` (partial — `tomlValue`, `formatTomlKey`)
- Create: `config_test.go` (partial)

- [ ] **Step 1: Write the failing test**

`config_test.go`:
```go
package codex

import "testing"

func TestTomlValue_Primitives(t *testing.T) {
	cases := []struct {
		in   any
		want string
	}{
		{"hello", `"hello"`},
		{`he said "hi"`, `"he said \"hi\""`},
		{int64(42), "42"},
		{42, "42"}, // also int
		{1.5, "1.5"},
		{true, "true"},
		{false, "false"},
	}
	for _, c := range cases {
		got, err := tomlValue(c.in, "x")
		if err != nil {
			t.Errorf("tomlValue(%v) error: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("tomlValue(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTomlValue_Array(t *testing.T) {
	got, err := tomlValue([]any{"a", "b", 1}, "arr")
	if err != nil {
		t.Fatal(err)
	}
	want := `["a", "b", 1]`
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestTomlValue_InlineTable(t *testing.T) {
	got, err := tomlValue(map[string]any{"foo": "bar", "n": 1}, "obj")
	if err != nil {
		t.Fatal(err)
	}
	// map iteration order varies — accept both orderings.
	want1 := `{foo = "bar", n = 1}`
	want2 := `{n = 1, foo = "bar"}`
	if got != want1 && got != want2 {
		t.Errorf("got %q, want %q or %q", got, want1, want2)
	}
}

func TestTomlValue_RejectsNil(t *testing.T) {
	if _, err := tomlValue(nil, "x"); err == nil {
		t.Error("expected error for nil")
	}
}

func TestTomlValue_RejectsNonFinite(t *testing.T) {
	cases := []float64{1.0 / 0, -1.0 / 0, 0.0 / 0}
	for _, c := range cases {
		if _, err := tomlValue(c, "x"); err == nil {
			t.Errorf("expected error for %v", c)
		}
	}
}

func TestFormatTomlKey(t *testing.T) {
	cases := map[string]string{
		"foo":     "foo",
		"foo_bar": "foo_bar",
		"foo-bar": "foo-bar",
		"FOO123":  "FOO123",
		"with space": `"with space"`,
		"":         `""`,
		"foo.bar":  `"foo.bar"`,
	}
	for in, want := range cases {
		if got := formatTomlKey(in); got != want {
			t.Errorf("formatTomlKey(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'TomlValue|FormatTomlKey'`
Expected: FAIL undefined.

- [ ] **Step 3: Implement config.go (partial)**

`config.go`:
```go
package codex

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
)

// tomlValue mirrors TS `toTomlValue` (exec.ts:262-296). Renders a Go value
// as a TOML literal suitable for `codex --config key=<value>`.
func tomlValue(v any, path string) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", fmt.Errorf("config override at %s cannot be null", path)
	case string:
		return strconv.Quote(x), nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	case int:
		return strconv.FormatInt(int64(x), 10), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return "", fmt.Errorf("config override at %s must be a finite number", path)
		}
		return strconv.FormatFloat(x, 'g', -1, 64), nil
	case []any:
		parts := make([]string, len(x))
		for i, elem := range x {
			s, err := tomlValue(elem, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return "", err
			}
			parts[i] = s
		}
		return "[" + joinComma(parts) + "]", nil
	case map[string]any:
		// Sort keys for deterministic output across map iteration order.
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(x))
		for _, k := range keys {
			child := x[k]
			if k == "" {
				return "", fmt.Errorf("config override keys must be non-empty strings")
			}
			if child == nil {
				continue
			}
			s, err := tomlValue(child, path+"."+k)
			if err != nil {
				return "", err
			}
			parts = append(parts, formatTomlKey(k)+" = "+s)
		}
		return "{" + joinComma(parts) + "}", nil
	default:
		return "", fmt.Errorf("unsupported config override value at %s: %T", path, v)
	}
}

var tomlBareKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// formatTomlKey mirrors TS `formatTomlKey` (exec.ts:309-313).
func formatTomlKey(k string) string {
	if tomlBareKey.MatchString(k) {
		return k
	}
	return strconv.Quote(k)
}

func joinComma(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}
```

**Note on map ordering:** TS uses `Object.entries` which is insertion-ordered;
Go map iteration is randomized. We sort keys alphabetically for deterministic
output — this is wire-equivalent for `codex --config`, which doesn't care
about key order. This is **NOT** an intentional divergence (no behavior
change observable to codex), just an implementation note.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run 'TomlValue|FormatTomlKey' -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add config.go config_test.go
git commit -m "feat: tomlValue + formatTomlKey (TS exec.ts:262-313 port)"
```

---

## Task 6: Config flattening

**Files:**
- Modify: `config.go` (add `serializeConfigOverrides`, `flattenConfigOverrides`)
- Modify: `config_test.go` (add tests)

- [ ] **Step 1: Append failing test to config_test.go**

Append to `config_test.go`:
```go
func TestSerializeConfigOverrides_Empty(t *testing.T) {
	got, err := serializeConfigOverrides(nil)
	if err != nil { t.Fatal(err) }
	if len(got) != 0 { t.Errorf("got %v, want empty", got) }

	got, err = serializeConfigOverrides(map[string]any{})
	if err != nil { t.Fatal(err) }
	if len(got) != 0 { t.Errorf("got %v, want empty", got) }
}

func TestSerializeConfigOverrides_Flat(t *testing.T) {
	got, err := serializeConfigOverrides(map[string]any{
		"model": "o3",
		"n":     42,
	})
	if err != nil { t.Fatal(err) }
	want := []string{`model="o3"`, "n=42"}
	if !equalStringSlice(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestSerializeConfigOverrides_Nested(t *testing.T) {
	got, err := serializeConfigOverrides(map[string]any{
		"sandbox_workspace_write": map[string]any{
			"network_access": true,
		},
	})
	if err != nil { t.Fatal(err) }
	want := []string{"sandbox_workspace_write.network_access=true"}
	if !equalStringSlice(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestSerializeConfigOverrides_DeepNested(t *testing.T) {
	got, err := serializeConfigOverrides(map[string]any{
		"a": map[string]any{
			"b": map[string]any{
				"c": "deep",
			},
		},
	})
	if err != nil { t.Fatal(err) }
	want := []string{`a.b.c="deep"`}
	if !equalStringSlice(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestSerializeConfigOverrides_EmptyNested(t *testing.T) {
	got, err := serializeConfigOverrides(map[string]any{
		"empty_table": map[string]any{},
	})
	if err != nil { t.Fatal(err) }
	want := []string{"empty_table={}"}
	if !equalStringSlice(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestSerializeConfigOverrides_SkipsNil(t *testing.T) {
	got, err := serializeConfigOverrides(map[string]any{
		"keep": "x",
		"skip": nil,
	})
	if err != nil { t.Fatal(err) }
	want := []string{`keep="x"`}
	if !equalStringSlice(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestSerializeConfigOverrides_RejectsEmptyKey(t *testing.T) {
	_, err := serializeConfigOverrides(map[string]any{"": "x"})
	if err == nil { t.Error("expected error for empty key") }
}

// equalStringSlice ignores order (Go map iteration is random).
func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) { return false }
	count := map[string]int{}
	for _, s := range a { count[s]++ }
	for _, s := range b { count[s]-- }
	for _, n := range count { if n != 0 { return false } }
	return true
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'SerializeConfigOverrides'`
Expected: FAIL undefined function.

- [ ] **Step 3: Append to config.go**

Append to `config.go`:
```go
// serializeConfigOverrides mirrors TS `serializeConfigOverrides`
// (exec.ts:230-240). Top-level call with nil/empty returns empty slice.
func serializeConfigOverrides(cfg map[string]any) ([]string, error) {
	if len(cfg) == 0 {
		return nil, nil
	}
	var out []string
	if err := flattenConfigOverrides(cfg, "", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// flattenConfigOverrides mirrors TS `flattenConfigOverrides`
// (exec.ts:242-260).
func flattenConfigOverrides(value any, prefix string, out *[]string) error {
	m, isMap := value.(map[string]any)
	if !isMap {
		if prefix == "" {
			return fmt.Errorf("codex config overrides must be a plain object")
		}
		s, err := tomlValue(value, prefix)
		if err != nil {
			return err
		}
		*out = append(*out, prefix+"="+s)
		return nil
	}
	if prefix != "" && len(m) == 0 {
		*out = append(*out, prefix+"={}")
		return nil
	}
	// Sort keys for determinism (see note in tomlValue).
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		child := m[k]
		if k == "" {
			return fmt.Errorf("codex config override keys must be non-empty strings")
		}
		if child == nil {
			continue
		}
		var path string
		if prefix == "" {
			path = k
		} else {
			path = prefix + "." + k
		}
		if _, isChildMap := child.(map[string]any); isChildMap {
			if err := flattenConfigOverrides(child, path, out); err != nil {
				return err
			}
		} else {
			s, err := tomlValue(child, path)
			if err != nil {
				return err
			}
			*out = append(*out, path+"="+s)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run 'SerializeConfigOverrides' -v`
Expected: all 7 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add config.go config_test.go
git commit -m "feat: serializeConfigOverrides + flattenConfigOverrides (TS exec.ts:230-260 port)"
```

---

## Task 7: ThreadEvent types and parser

**Files:**
- Create: `events.go`
- Create: `events_test.go`

- [ ] **Step 1: Write the failing test**

`events_test.go`:
```go
package codex

import (
	"encoding/json"
	"testing"
)

func TestParseEvent_ThreadStarted(t *testing.T) {
	line := `{"type":"thread.started","thread_id":"01HM..."}`
	evt, err := parseEvent([]byte(line))
	if err != nil { t.Fatal(err) }
	ts, ok := evt.(*ThreadStartedEvent)
	if !ok { t.Fatalf("got %T", evt) }
	if ts.ThreadID != "01HM..." { t.Errorf("ThreadID = %q", ts.ThreadID) }
}

func TestParseEvent_TurnStarted(t *testing.T) {
	evt, err := parseEvent([]byte(`{"type":"turn.started"}`))
	if err != nil { t.Fatal(err) }
	if _, ok := evt.(*TurnStartedEvent); !ok {
		t.Fatalf("got %T", evt)
	}
}

func TestParseEvent_TurnCompleted(t *testing.T) {
	line := `{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":5,"output_tokens":20,"reasoning_output_tokens":3}}`
	evt, err := parseEvent([]byte(line))
	if err != nil { t.Fatal(err) }
	tc, ok := evt.(*TurnCompletedEvent)
	if !ok { t.Fatalf("got %T", evt) }
	want := Usage{InputTokens: 10, CachedInputTokens: 5, OutputTokens: 20, ReasoningOutputTokens: 3}
	if tc.Usage != want { t.Errorf("Usage = %+v want %+v", tc.Usage, want) }
}

func TestParseEvent_TurnFailed(t *testing.T) {
	line := `{"type":"turn.failed","error":{"message":"model rejected"}}`
	evt, err := parseEvent([]byte(line))
	if err != nil { t.Fatal(err) }
	tf, ok := evt.(*TurnFailedEvent)
	if !ok { t.Fatalf("got %T", evt) }
	if tf.Error.Message != "model rejected" { t.Errorf("msg = %q", tf.Error.Message) }
}

func TestParseEvent_ThreadError(t *testing.T) {
	evt, err := parseEvent([]byte(`{"type":"error","message":"explode"}`))
	if err != nil { t.Fatal(err) }
	te, ok := evt.(*ThreadErrorEvent)
	if !ok { t.Fatalf("got %T", evt) }
	if te.Message != "explode" { t.Errorf("msg = %q", te.Message) }
}

func TestParseEvent_UnknownType(t *testing.T) {
	evt, err := parseEvent([]byte(`{"type":"future.event","x":1}`))
	if err != nil { t.Fatal(err) }
	u, ok := evt.(*UnknownEvent)
	if !ok { t.Fatalf("got %T", evt) }
	if u.Type != "future.event" { t.Errorf("Type = %q", u.Type) }
	// Raw should preserve the original bytes.
	if string(u.Raw) == "" { t.Error("Raw should be populated") }
}

func TestParseEvent_Malformed(t *testing.T) {
	_, err := parseEvent([]byte(`{not json`))
	if err == nil { t.Error("expected error for malformed JSON") }
	pe, ok := err.(*ParseEventError)
	if !ok { t.Fatalf("got %T", err) }
	if pe.Line == "" { t.Error("Line should be populated") }
}

func TestParseEvent_ItemDelegated(t *testing.T) {
	// item.* events delegate item parsing to parseItem (Task 8). For now,
	// assert that they're produced with an item field populated as raw if
	// the item parser is unavailable. We re-test in Task 8 with full item
	// types.
	line := `{"type":"item.started","item":{"id":"i1","type":"reasoning","text":"think"}}`
	evt, err := parseEvent([]byte(line))
	if err != nil { t.Fatal(err) }
	is, ok := evt.(*ItemStartedEvent)
	if !ok { t.Fatalf("got %T", evt) }
	if is.Item == nil { t.Error("Item should be non-nil") }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'ParseEvent'`
Expected: FAIL undefined.

- [ ] **Step 3: Implement events.go**

`events.go`:
```go
package codex

import (
	"encoding/json"
)

// ThreadEvent is a sealed interface implemented by every event variant.
// Mirror of TS `ThreadEvent` discriminated union (events.ts).
type ThreadEvent interface{ threadEvent() }

// Usage mirrors TS `Usage`.
type Usage struct {
	InputTokens           int `json:"input_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens"`
	OutputTokens          int `json:"output_tokens"`
	ReasoningOutputTokens int `json:"reasoning_output_tokens"`
}

// ThreadError mirrors TS `ThreadError`.
type ThreadError struct {
	Message string `json:"message"`
}

type ThreadStartedEvent struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"`
}

type TurnStartedEvent struct {
	Type string `json:"type"`
}

type TurnCompletedEvent struct {
	Type  string `json:"type"`
	Usage Usage  `json:"usage"`
}

type TurnFailedEvent struct {
	Type  string      `json:"type"`
	Error ThreadError `json:"error"`
}

type ItemStartedEvent struct {
	Type string     `json:"type"`
	Item ThreadItem `json:"-"`
	raw  json.RawMessage
}

type ItemUpdatedEvent struct {
	Type string     `json:"type"`
	Item ThreadItem `json:"-"`
	raw  json.RawMessage
}

type ItemCompletedEvent struct {
	Type string     `json:"type"`
	Item ThreadItem `json:"-"`
	raw  json.RawMessage
}

type ThreadErrorEvent struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// UnknownEvent is emitted when a JSONL line has a "type" field whose value
// the SDK does not recognize. Forward-compat: codex CLI may add new event
// types ahead of the SDK.
type UnknownEvent struct {
	Type string
	Raw  json.RawMessage
}

func (*ThreadStartedEvent) threadEvent() {}
func (*TurnStartedEvent) threadEvent()   {}
func (*TurnCompletedEvent) threadEvent() {}
func (*TurnFailedEvent) threadEvent()    {}
func (*ItemStartedEvent) threadEvent()   {}
func (*ItemUpdatedEvent) threadEvent()   {}
func (*ItemCompletedEvent) threadEvent() {}
func (*ThreadErrorEvent) threadEvent()   {}
func (*UnknownEvent) threadEvent()       {}

// parseEvent decodes one JSONL line from codex stdout. Returns
// *ParseEventError on JSON failure; otherwise a typed event (possibly
// *UnknownEvent for forward-compat).
func parseEvent(line []byte) (ThreadEvent, error) {
	var head struct {
		Type string          `json:"type"`
		Item json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal(line, &head); err != nil {
		return nil, &ParseEventError{Line: string(line), Err: err}
	}
	switch head.Type {
	case "thread.started":
		var e ThreadStartedEvent
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, &ParseEventError{Line: string(line), Err: err}
		}
		return &e, nil
	case "turn.started":
		var e TurnStartedEvent
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, &ParseEventError{Line: string(line), Err: err}
		}
		return &e, nil
	case "turn.completed":
		var e TurnCompletedEvent
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, &ParseEventError{Line: string(line), Err: err}
		}
		return &e, nil
	case "turn.failed":
		var e TurnFailedEvent
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, &ParseEventError{Line: string(line), Err: err}
		}
		return &e, nil
	case "item.started", "item.updated", "item.completed":
		item, err := parseItem(head.Item)
		if err != nil {
			return nil, &ParseEventError{Line: string(line), Err: err}
		}
		switch head.Type {
		case "item.started":
			return &ItemStartedEvent{Type: head.Type, Item: item, raw: head.Item}, nil
		case "item.updated":
			return &ItemUpdatedEvent{Type: head.Type, Item: item, raw: head.Item}, nil
		default:
			return &ItemCompletedEvent{Type: head.Type, Item: item, raw: head.Item}, nil
		}
	case "error":
		var e ThreadErrorEvent
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, &ParseEventError{Line: string(line), Err: err}
		}
		return &e, nil
	default:
		return &UnknownEvent{Type: head.Type, Raw: append([]byte(nil), line...)}, nil
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run 'ParseEvent' -v`
Expected: all PASS (TestParseEvent_ItemDelegated may need parseItem stub
— if the build fails because `parseItem` is undefined, add a stub at
the bottom of `events.go`):

```go
// parseItem stub — implemented in Task 8 (items.go). Until then, return
// UnknownItem so events.go alone compiles.
func parseItem(raw json.RawMessage) (ThreadItem, error) {
	return &UnknownItem{Raw: append([]byte(nil), raw...)}, nil
}
```

And `ThreadItem` / `UnknownItem` minimal stubs at the bottom of events.go
that get superseded in Task 8:
```go
// Stubs replaced in Task 8.
type ThreadItem interface{ threadItem() }
type UnknownItem struct {
	Type string
	Raw  json.RawMessage
}
func (*UnknownItem) threadItem() {}
```

Re-run: `go test ./... -run 'ParseEvent' -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add events.go events_test.go
git commit -m "feat: ThreadEvent variants + parseEvent (TS events.ts port)"
```

---

## Task 8: ThreadItem types and parser

**Files:**
- Create: `items.go`
- Create: `items_test.go`
- Modify: `events.go` (remove the Task 7 stubs of ThreadItem / UnknownItem / parseItem)

- [ ] **Step 1: Write the failing test**

`items_test.go`:
```go
package codex

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestParseItem_AgentMessage(t *testing.T) {
	raw := json.RawMessage(`{"id":"i1","type":"agent_message","text":"hi"}`)
	item, err := parseItem(raw)
	if err != nil { t.Fatal(err) }
	am, ok := item.(*AgentMessageItem)
	if !ok { t.Fatalf("got %T", item) }
	if am.ID != "i1" || am.Text != "hi" {
		t.Errorf("got %+v", am)
	}
}

func TestParseItem_Reasoning(t *testing.T) {
	raw := json.RawMessage(`{"id":"i2","type":"reasoning","text":"thinking..."}`)
	item, err := parseItem(raw)
	if err != nil { t.Fatal(err) }
	r, ok := item.(*ReasoningItem)
	if !ok { t.Fatalf("got %T", item) }
	if r.Text != "thinking..." { t.Errorf("text = %q", r.Text) }
}

func TestParseItem_CommandExecution(t *testing.T) {
	raw := json.RawMessage(`{"id":"c1","type":"command_execution","command":"ls","aggregated_output":"a\nb","exit_code":0,"status":"completed"}`)
	item, err := parseItem(raw)
	if err != nil { t.Fatal(err) }
	c, ok := item.(*CommandExecutionItem)
	if !ok { t.Fatalf("got %T", item) }
	if c.Command != "ls" || c.AggregatedOutput != "a\nb" || c.Status != "completed" {
		t.Errorf("got %+v", c)
	}
	if c.ExitCode == nil || *c.ExitCode != 0 {
		t.Errorf("ExitCode = %v", c.ExitCode)
	}
}

func TestParseItem_FileChange(t *testing.T) {
	raw := json.RawMessage(`{"id":"f1","type":"file_change","status":"completed","changes":[{"path":"a.go","kind":"add"},{"path":"b.go","kind":"update"}]}`)
	item, err := parseItem(raw)
	if err != nil { t.Fatal(err) }
	f, ok := item.(*FileChangeItem)
	if !ok { t.Fatalf("got %T", item) }
	if len(f.Changes) != 2 || f.Changes[0].Path != "a.go" || f.Changes[1].Kind != "update" {
		t.Errorf("got %+v", f)
	}
}

func TestParseItem_McpToolCall(t *testing.T) {
	raw := json.RawMessage(`{"id":"m1","type":"mcp_tool_call","server":"s","tool":"t","arguments":{"k":1},"status":"completed"}`)
	item, err := parseItem(raw)
	if err != nil { t.Fatal(err) }
	m, ok := item.(*McpToolCallItem)
	if !ok { t.Fatalf("got %T", item) }
	if m.Server != "s" || m.Tool != "t" || m.Status != "completed" {
		t.Errorf("got %+v", m)
	}
	args, _ := m.Arguments.(map[string]any)
	if args == nil || args["k"].(float64) != 1 {
		t.Errorf("Arguments = %v", m.Arguments)
	}
}

func TestParseItem_WebSearch(t *testing.T) {
	raw := json.RawMessage(`{"id":"w1","type":"web_search","query":"go generics"}`)
	item, err := parseItem(raw)
	if err != nil { t.Fatal(err) }
	w, ok := item.(*WebSearchItem)
	if !ok { t.Fatalf("got %T", item) }
	if w.Query != "go generics" { t.Errorf("Query = %q", w.Query) }
}

func TestParseItem_TodoList(t *testing.T) {
	raw := json.RawMessage(`{"id":"t1","type":"todo_list","items":[{"text":"a","completed":false},{"text":"b","completed":true}]}`)
	item, err := parseItem(raw)
	if err != nil { t.Fatal(err) }
	tl, ok := item.(*TodoListItem)
	if !ok { t.Fatalf("got %T", item) }
	want := []TodoItem{{Text: "a"}, {Text: "b", Completed: true}}
	if !reflect.DeepEqual(tl.Items, want) {
		t.Errorf("Items = %+v want %+v", tl.Items, want)
	}
}

func TestParseItem_Error(t *testing.T) {
	raw := json.RawMessage(`{"id":"e1","type":"error","message":"oops"}`)
	item, err := parseItem(raw)
	if err != nil { t.Fatal(err) }
	e, ok := item.(*ErrorItem)
	if !ok { t.Fatalf("got %T", item) }
	if e.Message != "oops" { t.Errorf("Message = %q", e.Message) }
}

func TestParseItem_UnknownType(t *testing.T) {
	raw := json.RawMessage(`{"id":"x","type":"future_item","weird":42}`)
	item, err := parseItem(raw)
	if err != nil { t.Fatal(err) }
	u, ok := item.(*UnknownItem)
	if !ok { t.Fatalf("got %T", item) }
	if u.Type != "future_item" { t.Errorf("Type = %q", u.Type) }
	if string(u.Raw) == "" { t.Error("Raw empty") }
}

func TestParseItem_Malformed(t *testing.T) {
	_, err := parseItem(json.RawMessage(`not json`))
	if err == nil { t.Error("expected error") }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'ParseItem'`
Expected: FAIL undefined item types.

- [ ] **Step 3: Remove stubs from events.go**

Open `events.go`, delete the stubs added at the bottom in Task 7 (the
`ThreadItem`, `UnknownItem`, `parseItem` declarations). Leave the
`parseEvent` body unchanged — `items.go` provides them now.

- [ ] **Step 4: Implement items.go**

`items.go`:
```go
package codex

import (
	"encoding/json"
	"fmt"
)

// ThreadItem is a sealed interface implemented by every item variant.
// Mirror of TS `ThreadItem` discriminated union (items.ts).
type ThreadItem interface{ threadItem() }

type AgentMessageItem struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Text string `json:"text"`
}

type ReasoningItem struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Text string `json:"text"`
}

// CommandExecutionStatus mirrors TS.
type CommandExecutionStatus string

const (
	CmdInProgress CommandExecutionStatus = "in_progress"
	CmdCompleted  CommandExecutionStatus = "completed"
	CmdFailed     CommandExecutionStatus = "failed"
)

type CommandExecutionItem struct {
	ID               string                 `json:"id"`
	Type             string                 `json:"type"`
	Command          string                 `json:"command"`
	AggregatedOutput string                 `json:"aggregated_output"`
	ExitCode         *int                   `json:"exit_code,omitempty"`
	Status           CommandExecutionStatus `json:"status"`
}

type PatchChangeKind string

const (
	PatchAdd    PatchChangeKind = "add"
	PatchDelete PatchChangeKind = "delete"
	PatchUpdate PatchChangeKind = "update"
)

type FileUpdateChange struct {
	Path string          `json:"path"`
	Kind PatchChangeKind `json:"kind"`
}

type PatchApplyStatus string

const (
	PatchCompleted PatchApplyStatus = "completed"
	PatchFailed    PatchApplyStatus = "failed"
)

type FileChangeItem struct {
	ID      string             `json:"id"`
	Type    string             `json:"type"`
	Changes []FileUpdateChange `json:"changes"`
	Status  PatchApplyStatus   `json:"status"`
}

type McpToolCallStatus string

const (
	McpInProgress McpToolCallStatus = "in_progress"
	McpCompleted  McpToolCallStatus = "completed"
	McpFailed     McpToolCallStatus = "failed"
)

type McpToolCallItem struct {
	ID        string            `json:"id"`
	Type      string            `json:"type"`
	Server    string            `json:"server"`
	Tool      string            `json:"tool"`
	Arguments any               `json:"arguments"`
	Result    *McpToolCallResult `json:"result,omitempty"`
	Error     *McpToolCallError  `json:"error,omitempty"`
	Status    McpToolCallStatus  `json:"status"`
}

type McpToolCallResult struct {
	// Content is the MCP server's content blocks. Left as raw JSON because
	// modeling MCP content blocks fully would re-implement
	// @modelcontextprotocol/sdk; consumers can decode further if needed.
	Content           json.RawMessage `json:"content"`
	StructuredContent any             `json:"structured_content"`
}

type McpToolCallError struct {
	Message string `json:"message"`
}

type WebSearchItem struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Query string `json:"query"`
}

type TodoItem struct {
	Text      string `json:"text"`
	Completed bool   `json:"completed"`
}

type TodoListItem struct {
	ID    string     `json:"id"`
	Type  string     `json:"type"`
	Items []TodoItem `json:"items"`
}

type ErrorItem struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Message string `json:"message"`
}

// UnknownItem is emitted when an item.* event carries a "type" the SDK
// does not recognize. Forward-compat.
type UnknownItem struct {
	Type string
	Raw  json.RawMessage
}

func (*AgentMessageItem) threadItem()     {}
func (*ReasoningItem) threadItem()        {}
func (*CommandExecutionItem) threadItem() {}
func (*FileChangeItem) threadItem()       {}
func (*McpToolCallItem) threadItem()      {}
func (*WebSearchItem) threadItem()        {}
func (*TodoListItem) threadItem()         {}
func (*ErrorItem) threadItem()            {}
func (*UnknownItem) threadItem()          {}

// parseItem decodes one item JSON object. Returns *UnknownItem for unknown
// types (forward-compat) and an error for malformed JSON or schema errors
// inside a known type.
func parseItem(raw json.RawMessage) (ThreadItem, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("parseItem: empty raw")
	}
	var head struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("parseItem head: %w", err)
	}
	switch head.Type {
	case "agent_message":
		var v AgentMessageItem
		return &v, json.Unmarshal(raw, &v)
	case "reasoning":
		var v ReasoningItem
		return &v, json.Unmarshal(raw, &v)
	case "command_execution":
		var v CommandExecutionItem
		return &v, json.Unmarshal(raw, &v)
	case "file_change":
		var v FileChangeItem
		return &v, json.Unmarshal(raw, &v)
	case "mcp_tool_call":
		var v McpToolCallItem
		return &v, json.Unmarshal(raw, &v)
	case "web_search":
		var v WebSearchItem
		return &v, json.Unmarshal(raw, &v)
	case "todo_list":
		var v TodoListItem
		return &v, json.Unmarshal(raw, &v)
	case "error":
		var v ErrorItem
		return &v, json.Unmarshal(raw, &v)
	default:
		return &UnknownItem{Type: head.Type, Raw: append([]byte(nil), raw...)}, nil
	}
}
```

- [ ] **Step 5: Run all tests to verify nothing regressed**

Run: `go test ./... -v`
Expected: all events_test + items_test + earlier tests PASS.

- [ ] **Step 6: Commit**

```bash
git add items.go items_test.go events.go
git commit -m "feat: ThreadItem variants + parseItem (TS items.ts port)"
```

---

## Task 9: OutputSchema temp file lifecycle

**Files:**
- Create: `schema.go`
- Create: `schema_test.go`

- [ ] **Step 1: Write the failing test**

`schema_test.go`:
```go
package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPrepareOutputSchema_Nil(t *testing.T) {
	path, cleanup, err := prepareOutputSchema(nil)
	if err != nil { t.Fatal(err) }
	if path != "" { t.Errorf("path = %q, want empty", path) }
	if cleanup == nil { t.Error("cleanup should be non-nil even when nil schema") }
	cleanup() // should not panic
}

func TestPrepareOutputSchema_Map(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"x": map[string]any{"type": "string"},
		},
	}
	path, cleanup, err := prepareOutputSchema(schema)
	if err != nil { t.Fatal(err) }
	defer cleanup()

	if !strings.HasSuffix(path, "schema.json") {
		t.Errorf("path = %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("temp file should exist: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil { t.Fatal(err) }
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil { t.Fatal(err) }
	if got["type"] != "object" { t.Errorf("got %v", got) }

	// Cleanup removes the dir
	dir := filepath.Dir(path)
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("dir should be gone after cleanup, stat err = %v", err)
	}
}

func TestPrepareOutputSchema_RejectsNonObject(t *testing.T) {
	for _, bad := range []any{42, "string", []any{1, 2}, true} {
		_, _, err := prepareOutputSchema(bad)
		if err == nil {
			t.Errorf("expected error for %v (%T)", bad, bad)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'PrepareOutputSchema'`
Expected: FAIL undefined function.

- [ ] **Step 3: Implement schema.go**

`schema.go`:
```go
package codex

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
)

// prepareOutputSchema mirrors TS `createOutputSchemaFile`
// (outputSchemaFile.ts:6-37). Writes the schema to a fresh tempdir under
// os.TempDir() and returns (path, cleanup, error). cleanup is always
// safe to call (no-op if schema was nil) and removes the entire tempdir.
func prepareOutputSchema(schema any) (path string, cleanup func(), err error) {
	noop := func() {}
	if schema == nil {
		return "", noop, nil
	}
	if !isJSONObject(schema) {
		return "", noop, errors.New("OutputSchema must be a JSON object (map or struct)")
	}
	dir, err := os.MkdirTemp("", "codex-output-schema-")
	if err != nil {
		return "", noop, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	path = filepath.Join(dir, "schema.json")
	data, mErr := json.Marshal(schema)
	if mErr != nil {
		cleanup()
		return "", noop, mErr
	}
	if wErr := os.WriteFile(path, data, 0o600); wErr != nil {
		cleanup()
		return "", noop, wErr
	}
	return path, cleanup, nil
}

// isJSONObject mirrors TS `isJsonObject` (outputSchemaFile.ts:39-41).
// Accepts maps and structs; rejects scalars, slices, arrays, nil.
func isJSONObject(v any) bool {
	if v == nil {
		return false
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Map, reflect.Struct:
		return true
	case reflect.Ptr:
		if rv.IsNil() {
			return false
		}
		return isJSONObject(rv.Elem().Interface())
	default:
		return false
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run 'PrepareOutputSchema' -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add schema.go schema_test.go
git commit -m "feat: prepareOutputSchema (TS outputSchemaFile.ts port)"
```

---

## Task 10: Arg builder (pure)

**Files:**
- Create: `exec.go` (partial — just `buildArgs`, `composeEnv`)
- Create: `exec_args_test.go`

- [ ] **Step 1: Write the failing test**

`exec_args_test.go`:
```go
package codex

import (
	"reflect"
	"testing"
)

func TestBuildArgs_Minimal(t *testing.T) {
	got, err := buildArgs(buildArgsInput{})
	if err != nil { t.Fatal(err) }
	want := []string{"exec", "--experimental-json"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestBuildArgs_BaseURL(t *testing.T) {
	got, err := buildArgs(buildArgsInput{
		CodexOpts: CodexOptions{BaseURL: "https://x.example/v1"},
	})
	if err != nil { t.Fatal(err) }
	want := []string{
		"exec", "--experimental-json",
		"--config", `openai_base_url="https://x.example/v1"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestBuildArgs_ConfigFlattenedFirst(t *testing.T) {
	got, err := buildArgs(buildArgsInput{
		CodexOpts: CodexOptions{Config: map[string]any{"a": "b"}},
		ThreadOpts: ThreadOptions{Model: "o3"},
	})
	if err != nil { t.Fatal(err) }
	// Config-from-CodexOptions comes BEFORE per-thread/per-turn flags.
	want := []string{
		"exec", "--experimental-json",
		"--config", `a="b"`,
		"--model", "o3",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestBuildArgs_AllThreadOptions(t *testing.T) {
	tt := true
	ff := false
	got, err := buildArgs(buildArgsInput{
		ThreadOpts: ThreadOptions{
			Model:                "o3",
			SandboxMode:          SandboxWorkspaceWrite,
			WorkingDirectory:     "/tmp/w",
			AdditionalDirs:       []string{"/d1", "/d2"},
			SkipGitRepoCheck:     true,
			ModelReasoningEffort: ReasoningHigh,
			NetworkAccessEnabled: &tt,
			WebSearchMode:        WebSearchLive,
			ApprovalPolicy:       ApprovalOnRequest,
		},
	})
	if err != nil { t.Fatal(err) }
	want := []string{
		"exec", "--experimental-json",
		"--model", "o3",
		"--sandbox", "workspace-write",
		"--cd", "/tmp/w",
		"--add-dir", "/d1",
		"--add-dir", "/d2",
		"--skip-git-repo-check",
		"--config", `model_reasoning_effort="high"`,
		"--config", "sandbox_workspace_write.network_access=true",
		"--config", `web_search="live"`,
		"--config", `approval_policy="on-request"`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\ngot:  %v\nwant: %v", got, want)
	}
	_ = ff
}

func TestBuildArgs_LegacyWebSearchEnabled(t *testing.T) {
	tt := true
	ff := false
	gotTrue, _ := buildArgs(buildArgsInput{
		ThreadOpts: ThreadOptions{WebSearchEnabled: &tt},
	})
	gotFalse, _ := buildArgs(buildArgsInput{
		ThreadOpts: ThreadOptions{WebSearchEnabled: &ff},
	})
	wantTrue := []string{"exec", "--experimental-json", "--config", `web_search="live"`}
	wantFalse := []string{"exec", "--experimental-json", "--config", `web_search="disabled"`}
	if !reflect.DeepEqual(gotTrue, wantTrue) {
		t.Errorf("true: got %v want %v", gotTrue, wantTrue)
	}
	if !reflect.DeepEqual(gotFalse, wantFalse) {
		t.Errorf("false: got %v want %v", gotFalse, wantFalse)
	}
}

func TestBuildArgs_WebSearchModeOverridesLegacy(t *testing.T) {
	tt := true
	got, _ := buildArgs(buildArgsInput{
		ThreadOpts: ThreadOptions{
			WebSearchMode:    WebSearchCached,
			WebSearchEnabled: &tt,
		},
	})
	want := []string{"exec", "--experimental-json", "--config", `web_search="cached"`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestBuildArgs_OutputSchema(t *testing.T) {
	got, _ := buildArgs(buildArgsInput{
		OutputSchemaPath: "/tmp/schema.json",
	})
	want := []string{"exec", "--experimental-json", "--output-schema", "/tmp/schema.json"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestBuildArgs_ResumeAndImagesOrdering(t *testing.T) {
	got, _ := buildArgs(buildArgsInput{
		ThreadID: "01HM-thread",
		Images:   []string{"/a.png", "/b.png"},
	})
	// Resume comes BEFORE images. Images attach to the `resume` subcommand.
	want := []string{
		"exec", "--experimental-json",
		"resume", "01HM-thread",
		"--image", "/a.png",
		"--image", "/b.png",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\ngot:  %v\nwant: %v", got, want)
	}
}

func TestBuildArgs_FullExample(t *testing.T) {
	tt := true
	got, err := buildArgs(buildArgsInput{
		CodexOpts: CodexOptions{
			BaseURL: "https://api.x.example/v1",
			Config:  map[string]any{"feature.x": true},
		},
		ThreadOpts: ThreadOptions{
			Model:                "o3",
			SandboxMode:          SandboxWorkspaceWrite,
			NetworkAccessEnabled: &tt,
			ApprovalPolicy:       ApprovalNever,
		},
		ThreadID:         "thread-1",
		Images:           []string{"/x.png"},
		OutputSchemaPath: "/tmp/s.json",
	})
	if err != nil { t.Fatal(err) }
	want := []string{
		"exec", "--experimental-json",
		"--config", "feature.x=true",
		"--config", `openai_base_url="https://api.x.example/v1"`,
		"--model", "o3",
		"--sandbox", "workspace-write",
		"--output-schema", "/tmp/s.json",
		"--config", "sandbox_workspace_write.network_access=true",
		"--config", `approval_policy="never"`,
		"resume", "thread-1",
		"--image", "/x.png",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("\ngot:  %v\nwant: %v", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'BuildArgs'`
Expected: FAIL undefined.

- [ ] **Step 3: Implement exec.go (partial)**

`exec.go`:
```go
package codex

// buildArgsInput collects all inputs to buildArgs for clean test wiring.
type buildArgsInput struct {
	CodexOpts        CodexOptions
	ThreadOpts       ThreadOptions
	ThreadID         string   // empty = no resume
	Images           []string // from joinTextParts
	OutputSchemaPath string   // empty = no --output-schema
}

// buildArgs constructs the argv for `codex exec`. The order mirrors TS
// exec.ts:73-148 line-for-line so behavior is bit-identical (modulo the
// listed divergences).
func buildArgs(in buildArgsInput) ([]string, error) {
	args := []string{"exec", "--experimental-json"}

	// 1. CodexOptions.Config — flatten and apply BEFORE per-thread flags
	overrides, err := serializeConfigOverrides(in.CodexOpts.Config)
	if err != nil {
		return nil, err
	}
	for _, o := range overrides {
		args = append(args, "--config", o)
	}

	// 2. baseUrl
	if in.CodexOpts.BaseURL != "" {
		quoted, _ := tomlValue(in.CodexOpts.BaseURL, "openai_base_url")
		args = append(args, "--config", "openai_base_url="+quoted)
	}

	// 3. model / sandbox / cwd / additional dirs / skip-git
	if in.ThreadOpts.Model != "" {
		args = append(args, "--model", in.ThreadOpts.Model)
	}
	if in.ThreadOpts.SandboxMode != "" {
		args = append(args, "--sandbox", string(in.ThreadOpts.SandboxMode))
	}
	if in.ThreadOpts.WorkingDirectory != "" {
		args = append(args, "--cd", in.ThreadOpts.WorkingDirectory)
	}
	for _, d := range in.ThreadOpts.AdditionalDirs {
		args = append(args, "--add-dir", d)
	}
	if in.ThreadOpts.SkipGitRepoCheck {
		args = append(args, "--skip-git-repo-check")
	}

	// 4. output-schema (after fs paths, before reasoning/web/approval)
	if in.OutputSchemaPath != "" {
		args = append(args, "--output-schema", in.OutputSchemaPath)
	}

	// 5. reasoning
	if in.ThreadOpts.ModelReasoningEffort != "" {
		args = append(args, "--config", `model_reasoning_effort="`+string(in.ThreadOpts.ModelReasoningEffort)+`"`)
	}

	// 6. network access
	if in.ThreadOpts.NetworkAccessEnabled != nil {
		v := "false"
		if *in.ThreadOpts.NetworkAccessEnabled {
			v = "true"
		}
		args = append(args, "--config", "sandbox_workspace_write.network_access="+v)
	}

	// 7. web search (mode wins over legacy enabled)
	switch {
	case in.ThreadOpts.WebSearchMode != "":
		args = append(args, "--config", `web_search="`+string(in.ThreadOpts.WebSearchMode)+`"`)
	case in.ThreadOpts.WebSearchEnabled != nil && *in.ThreadOpts.WebSearchEnabled:
		args = append(args, "--config", `web_search="live"`)
	case in.ThreadOpts.WebSearchEnabled != nil && !*in.ThreadOpts.WebSearchEnabled:
		args = append(args, "--config", `web_search="disabled"`)
	}

	// 8. approval policy
	if in.ThreadOpts.ApprovalPolicy != "" {
		args = append(args, "--config", `approval_policy="`+string(in.ThreadOpts.ApprovalPolicy)+`"`)
	}

	// 9. resume subcommand (must come AFTER exec flags, BEFORE images)
	if in.ThreadID != "" {
		args = append(args, "resume", in.ThreadID)
	}

	// 10. images (parsed by `resume` subcommand, OR by `exec` if no resume)
	for _, img := range in.Images {
		args = append(args, "--image", img)
	}

	return args, nil
}
```

**Note:** Config flattening in `serializeConfigOverrides` sorts keys
alphabetically for deterministic output. Tests that pass multiple Config
keys must use single-key maps OR assert order-insensitively (see Task 6).

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run 'BuildArgs' -v`
Expected: all 8 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add exec.go exec_args_test.go
git commit -m "feat: buildArgs (TS exec.ts:73-148 line-for-line port)"
```

---

## Task 11: Env composition (pure)

**Files:**
- Modify: `exec.go` (add `composeEnv`)
- Create: `exec_env_test.go`

- [ ] **Step 1: Write the failing test**

`exec_env_test.go`:
```go
package codex

import (
	"reflect"
	"testing"
)

func TestComposeEnv_NilUsesProcessEnv(t *testing.T) {
	procEnv := []string{"PATH=/usr/bin", "HOME=/h"}
	got := composeEnv(CodexOptions{}, procEnv)
	// Should contain PATH, HOME, plus originator default
	want := map[string]string{
		"PATH":                              "/usr/bin",
		"HOME":                              "/h",
		"CODEX_INTERNAL_ORIGINATOR_OVERRIDE": "codex_sdk_go",
	}
	if !reflect.DeepEqual(envToMap(got), want) {
		t.Errorf("got %v want %v", envToMap(got), want)
	}
}

func TestComposeEnv_OptOverridesProcess(t *testing.T) {
	procEnv := []string{"PATH=/usr/bin"}
	got := composeEnv(CodexOptions{
		Env: map[string]string{"FOO": "bar"},
	}, procEnv)
	want := map[string]string{
		"FOO":                                "bar",
		"CODEX_INTERNAL_ORIGINATOR_OVERRIDE": "codex_sdk_go",
	}
	// PATH should NOT be present — Env replaces process env entirely.
	if !reflect.DeepEqual(envToMap(got), want) {
		t.Errorf("got %v want %v", envToMap(got), want)
	}
}

func TestComposeEnv_PreservesUserOriginator(t *testing.T) {
	got := composeEnv(CodexOptions{
		Env: map[string]string{"CODEX_INTERNAL_ORIGINATOR_OVERRIDE": "custom"},
	}, nil)
	m := envToMap(got)
	if m["CODEX_INTERNAL_ORIGINATOR_OVERRIDE"] != "custom" {
		t.Errorf("originator = %q, want preserved", m["CODEX_INTERNAL_ORIGINATOR_OVERRIDE"])
	}
}

func TestComposeEnv_APIKeySetsCodexAPIKey(t *testing.T) {
	got := composeEnv(CodexOptions{APIKey: "sk-test"}, nil)
	m := envToMap(got)
	if m["CODEX_API_KEY"] != "sk-test" {
		t.Errorf("CODEX_API_KEY = %q", m["CODEX_API_KEY"])
	}
}

func TestComposeEnv_APIKeyOverridesProvidedEnv(t *testing.T) {
	got := composeEnv(CodexOptions{
		APIKey: "sk-new",
		Env:    map[string]string{"CODEX_API_KEY": "sk-old"},
	}, nil)
	m := envToMap(got)
	if m["CODEX_API_KEY"] != "sk-new" {
		t.Errorf("CODEX_API_KEY = %q, want sk-new", m["CODEX_API_KEY"])
	}
}

func envToMap(env []string) map[string]string {
	m := map[string]string{}
	for _, kv := range env {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				m[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return m
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'ComposeEnv'`
Expected: FAIL undefined.

- [ ] **Step 3: Append composeEnv to exec.go**

Append to `exec.go`:
```go
import "strings"

// composeEnv mirrors TS exec.ts:148-167. Returned slice is in
// "KEY=VALUE" form ready for cmd.Env.
//
// procEnv is normally os.Environ(); accepted as a parameter for testability.
func composeEnv(opts CodexOptions, procEnv []string) []string {
	env := map[string]string{}
	if opts.Env != nil {
		for k, v := range opts.Env {
			env[k] = v
		}
	} else {
		for _, kv := range procEnv {
			eq := strings.IndexByte(kv, '=')
			if eq < 0 {
				continue
			}
			env[kv[:eq]] = kv[eq+1:]
		}
	}
	if env["CODEX_INTERNAL_ORIGINATOR_OVERRIDE"] == "" {
		env["CODEX_INTERNAL_ORIGINATOR_OVERRIDE"] = "codex_sdk_go"
	}
	if opts.APIKey != "" {
		env["CODEX_API_KEY"] = opts.APIKey
	}
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}
```

**Note:** `import "strings"` may already be implicit in some file; ensure
the existing imports block in `exec.go` includes it. If `exec.go` had no
imports yet, replace the `package codex` line with:
```go
package codex

import "strings"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run 'ComposeEnv' -v`
Expected: all 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add exec.go exec_env_test.go
git commit -m "feat: composeEnv (TS exec.ts:148-167 port)"
```

---

## Task 12: Subprocess runner with bash-script fakes

**Files:**
- Modify: `exec.go` (add `runExec`)
- Create: `testdata/fake_codex/clean.sh`
- Create: `testdata/fake_codex/turn_failed.sh`
- Create: `testdata/fake_codex/exit_nonzero.sh`
- Create: `testdata/fake_codex/hang.sh`
- Create: `testdata/fake_codex/malformed.sh`
- Create: `exec_run_test.go`

- [ ] **Step 1: Create the fake codex scripts**

`testdata/fake_codex/clean.sh`:
```bash
#!/bin/bash
# Reads prompt on stdin, emits a complete clean turn on stdout.
cat > /dev/null
echo '{"type":"thread.started","thread_id":"01HMFAKE"}'
echo '{"type":"turn.started"}'
echo '{"type":"item.completed","item":{"id":"i1","type":"agent_message","text":"hello"}}'
echo '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":2,"reasoning_output_tokens":0}}'
exit 0
```

`testdata/fake_codex/turn_failed.sh`:
```bash
#!/bin/bash
cat > /dev/null
echo '{"type":"thread.started","thread_id":"01HMFAKE2"}'
echo '{"type":"turn.failed","error":{"message":"model rejected"}}'
exit 0
```

`testdata/fake_codex/exit_nonzero.sh`:
```bash
#!/bin/bash
cat > /dev/null
echo '{"type":"thread.started","thread_id":"01HMFAKE3"}'
echo "boom!" >&2
exit 7
```

`testdata/fake_codex/hang.sh`:
```bash
#!/bin/bash
cat > /dev/null
echo '{"type":"thread.started","thread_id":"01HMFAKE4"}'
# Trap SIGTERM so we can verify SIGKILL escalation.
trap 'echo "ignoring TERM" >&2' TERM
sleep 30
```

`testdata/fake_codex/malformed.sh`:
```bash
#!/bin/bash
cat > /dev/null
echo '{"type":"thread.started","thread_id":"01HMFAKE5"}'
echo '{not valid json'
echo '{"type":"turn.completed","usage":{"input_tokens":0,"cached_input_tokens":0,"output_tokens":0,"reasoning_output_tokens":0}}'
exit 0
```

Make all executable:
```bash
chmod +x testdata/fake_codex/*.sh
```

- [ ] **Step 2: Write failing test**

`exec_run_test.go`:
```go
package codex

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func fakeBin(t *testing.T, name string) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "testdata", "fake_codex", name)
}

func TestRunExec_Clean(t *testing.T) {
	stream, err := runExec(context.Background(), runExecInput{
		Binary: fakeBin(t, "clean.sh"),
		Args:   []string{"exec", "--experimental-json"},
		Env:    nil,
		Prompt: "hi",
	})
	if err != nil { t.Fatal(err) }

	var got []string
	for evt := range stream.Events() {
		switch e := evt.(type) {
		case *ThreadStartedEvent:
			got = append(got, "started:"+e.ThreadID)
		case *TurnStartedEvent:
			got = append(got, "turn.started")
		case *ItemCompletedEvent:
			if am, ok := e.Item.(*AgentMessageItem); ok {
				got = append(got, "msg:"+am.Text)
			}
		case *TurnCompletedEvent:
			got = append(got, "completed")
		}
	}
	if err := stream.Wait(); err != nil {
		t.Errorf("Wait() = %v, want nil", err)
	}

	want := []string{"started:01HMFAKE", "turn.started", "msg:hello", "completed"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Errorf("got[%d]=%q, want %q", i, got[i:], want[i:])
			break
		}
	}
}

func TestRunExec_TurnFailedYieldsButWaitNil(t *testing.T) {
	stream, err := runExec(context.Background(), runExecInput{
		Binary: fakeBin(t, "turn_failed.sh"),
		Args:   []string{"exec", "--experimental-json"},
	})
	if err != nil { t.Fatal(err) }

	sawFailed := false
	for evt := range stream.Events() {
		if _, ok := evt.(*TurnFailedEvent); ok {
			sawFailed = true
		}
	}
	if !sawFailed { t.Error("expected TurnFailedEvent on channel") }
	if err := stream.Wait(); err != nil {
		t.Errorf("Wait() = %v, want nil (turn.failed alone does not fail Wait)", err)
	}
}

func TestRunExec_NonZeroExit(t *testing.T) {
	stream, err := runExec(context.Background(), runExecInput{
		Binary: fakeBin(t, "exit_nonzero.sh"),
		Args:   []string{"exec", "--experimental-json"},
	})
	if err != nil { t.Fatal(err) }

	for range stream.Events() {} // drain
	werr := stream.Wait()
	if werr == nil {
		t.Fatal("expected error from Wait()")
	}
	var nz *NonZeroExitError
	if !errors.As(werr, &nz) {
		t.Fatalf("Wait err = %T (%v), want *NonZeroExitError", werr, werr)
	}
	if nz.Code != 7 {
		t.Errorf("Code = %d", nz.Code)
	}
	if nz.Stderr == "" {
		t.Errorf("Stderr should be populated, got empty")
	}
}

func TestRunExec_MalformedLineYieldsErrorEventNotWaitErr(t *testing.T) {
	stream, err := runExec(context.Background(), runExecInput{
		Binary: fakeBin(t, "malformed.sh"),
		Args:   []string{"exec", "--experimental-json"},
	})
	if err != nil { t.Fatal(err) }

	sawErr := false
	sawCompleted := false
	for evt := range stream.Events() {
		if _, ok := evt.(*ThreadErrorEvent); ok {
			sawErr = true
		}
		if _, ok := evt.(*TurnCompletedEvent); ok {
			sawCompleted = true
		}
	}
	if !sawErr      { t.Error("expected synthetic ThreadErrorEvent for malformed line") }
	if !sawCompleted { t.Error("expected scanner to continue past bad line") }
	if err := stream.Wait(); err != nil {
		t.Errorf("Wait() = %v, want nil", err)
	}
}

func TestRunExec_CtxCancelEscalatesToKill(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := runExec(ctx, runExecInput{
		Binary: fakeBin(t, "hang.sh"),
		Args:   []string{"exec", "--experimental-json"},
	})
	if err != nil { t.Fatal(err) }

	// Read thread.started, then cancel.
	saw := 0
	go func() {
		for range stream.Events() {
			saw++
			if saw == 1 {
				cancel()
			}
		}
	}()

	start := time.Now()
	werr := stream.Wait()
	elapsed := time.Since(start)
	if !errors.Is(werr, context.Canceled) {
		// Ctx cancel may surface as ctx.Err(), or as NonZeroExitError if
		// exec.Cmd reports the SIGKILL exit. Accept either, but assert it's
		// not a clean nil.
		var nz *NonZeroExitError
		if werr == nil || (!errors.As(werr, &nz) && !errors.Is(werr, context.Canceled)) {
			t.Errorf("Wait() = %v, want ctx.Canceled or NonZeroExitError", werr)
		}
	}
	// Should be well under 10s — proves SIGKILL escalation worked.
	if elapsed > 5*time.Second {
		t.Errorf("Wait took %v, expected <5s with SIGKILL escalation", elapsed)
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./... -run 'RunExec'`
Expected: FAIL undefined `runExec`.

- [ ] **Step 4: Append runExec to exec.go**

Add to `exec.go` imports:
```go
import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)
```

Append to `exec.go`:
```go
type runExecInput struct {
	Binary string
	Args   []string
	Env    []string // KEY=VALUE form; nil = inherit from os/exec default (current process)
	Prompt string   // written to stdin then closed
}

// stream is the internal *StreamedTurn that runExec returns.
type stream struct {
	events chan ThreadEvent
	waitMu sync.Mutex
	waitDone chan struct{}
	terminalErr error
}

func (s *stream) Events() <-chan ThreadEvent { return s.events }
func (s *stream) Wait() error {
	<-s.waitDone
	return s.terminalErr
}

const (
	stderrCap     = 64 * 1024
	scannerBufMax = 4 * 1024 * 1024
)

// runExec spawns the codex process, pipes prompt to stdin, parses JSONL on
// stdout into the events channel, captures stderr for diagnostics, and
// returns a *stream whose Wait() reports terminal status.
//
// Cancellation: SIGTERM on ctx.Done, escalating to SIGKILL after 2s via
// cmd.WaitDelay (Go 1.20+).
func runExec(ctx context.Context, in runExecInput) (*stream, error) {
	cmd := exec.CommandContext(ctx, in.Binary, in.Args...)
	cmd.Env = in.Env
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 2 * time.Second

	stdin, err := cmd.StdinPipe()
	if err != nil { return nil, &SpawnError{Err: fmt.Errorf("stdin pipe: %w", err)} }
	stdout, err := cmd.StdoutPipe()
	if err != nil { return nil, &SpawnError{Err: fmt.Errorf("stdout pipe: %w", err)} }
	stderr, err := cmd.StderrPipe()
	if err != nil { return nil, &SpawnError{Err: fmt.Errorf("stderr pipe: %w", err)} }

	if err := cmd.Start(); err != nil {
		return nil, &SpawnError{Err: err}
	}

	// Write prompt then close stdin.
	go func() {
		_, _ = io.WriteString(stdin, in.Prompt)
		_ = stdin.Close()
	}()

	// Drain stderr into a bounded buffer.
	var stderrBuf bytes.Buffer
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		buf := make([]byte, 4096)
		for {
			n, rerr := stderr.Read(buf)
			if n > 0 {
				if stderrBuf.Len() < stderrCap {
					space := stderrCap - stderrBuf.Len()
					if n > space { n = space }
					stderrBuf.Write(buf[:n])
					if stderrBuf.Len() == stderrCap {
						stderrBuf.WriteString("...[truncated]\n")
					}
				}
			}
			if rerr != nil { return }
		}
	}()

	s := &stream{
		events:   make(chan ThreadEvent, 16),
		waitDone: make(chan struct{}),
	}

	go func() {
		// Defers run LIFO. We MUST close events BEFORE waitDone so that
		// Wait() unblocking implies the events channel is fully closed
		// (per spec contract). Register waitDone first so it runs last.
		defer close(s.waitDone)
		defer close(s.events)

		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 0, 64*1024), scannerBufMax)
		for sc.Scan() {
			line := sc.Bytes()
			if len(strings.TrimSpace(string(line))) == 0 { continue }
			evt, perr := parseEvent(line)
			if perr != nil {
				// Wrap into synthetic ThreadErrorEvent; do not terminate.
				s.events <- &ThreadErrorEvent{Type: "error", Message: perr.Error()}
				continue
			}
			s.events <- evt
		}
		// Scanner exited (EOF or error). Drain stderr fully.
		<-stderrDone

		// Reap process, classify exit.
		werr := cmd.Wait()
		if werr == nil {
			return // clean exit, terminalErr stays nil
		}

		if ctx.Err() != nil {
			s.terminalErr = ctx.Err()
			s.events <- &ThreadErrorEvent{Type: "error", Message: "codex cancelled: " + ctx.Err().Error()}
			return
		}

		var ee *exec.ExitError
		if errors.As(werr, &ee) {
			ws, _ := ee.Sys().(syscall.WaitStatus)
			code := ee.ExitCode()
			signal := ""
			if ws.Signaled() {
				signal = ws.Signal().String()
			}
			nz := &NonZeroExitError{Code: code, Signal: signal, Stderr: stderrBuf.String()}
			s.terminalErr = nz
			s.events <- &ThreadErrorEvent{Type: "error", Message: nz.Error()}
			return
		}

		// Other Wait error.
		s.terminalErr = werr
		s.events <- &ThreadErrorEvent{Type: "error", Message: werr.Error()}
	}()

	return s, nil
}
```

Add `errors` to the import block at the top of exec.go.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./... -run 'RunExec' -v -timeout=30s`
Expected: all 5 tests PASS within 10s total.

- [ ] **Step 6: Commit**

```bash
git add exec.go testdata/fake_codex/*.sh exec_run_test.go
git commit -m "feat: runExec subprocess driver with SIGTERM->SIGKILL escalation"
```

---

## Task 13: Codex top-level + Thread (RunStreamed)

**Files:**
- Create: `codex.go`
- Create: `thread.go`
- Create: `thread_test.go`

- [ ] **Step 1: Write the failing test**

`thread_test.go`:
```go
package codex

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
)

func fakeCodex(t *testing.T, script string) *Codex {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	bin := filepath.Join(filepath.Dir(thisFile), "testdata", "fake_codex", script)
	return New(CodexOptions{BinaryPath: bin})
}

func TestStartThread_CapturesIDOnFirstStartedEvent(t *testing.T) {
	c := fakeCodex(t, "clean.sh")
	th := c.StartThread(ThreadOptions{})
	if th.ID() != "" {
		t.Errorf("ID before run = %q, want empty", th.ID())
	}

	stream, err := th.RunStreamed(context.Background(), StringInput("hi"), TurnOptions{})
	if err != nil { t.Fatal(err) }
	for range stream.Events() {} // drain
	if err := stream.Wait(); err != nil { t.Fatal(err) }

	if th.ID() != "01HMFAKE" {
		t.Errorf("ID after run = %q, want 01HMFAKE", th.ID())
	}
}

func TestResumeThread_PassesResumeArg(t *testing.T) {
	// Use a script that echoes back the args it received as a synthetic
	// agent_message item — proves the SDK appended `resume <id>`.
	bin := writeArgEchoScript(t)
	c := New(CodexOptions{BinaryPath: bin})
	th := c.ResumeThread("preset-id", ThreadOptions{})
	if th.ID() != "preset-id" {
		t.Errorf("ID before run = %q", th.ID())
	}

	stream, err := th.RunStreamed(context.Background(), StringInput("hi"), TurnOptions{})
	if err != nil { t.Fatal(err) }

	var msg string
	for evt := range stream.Events() {
		if ic, ok := evt.(*ItemCompletedEvent); ok {
			if am, ok := ic.Item.(*AgentMessageItem); ok {
				msg = am.Text
			}
		}
	}
	if err := stream.Wait(); err != nil { t.Fatal(err) }

	// Args echo should include "resume preset-id"
	if !contains(msg, "resume preset-id") {
		t.Errorf("args echo = %q, missing 'resume preset-id'", msg)
	}
}

func TestStartThread_SecondRunSwitchesToResume(t *testing.T) {
	bin := writeArgEchoScript(t)
	c := New(CodexOptions{BinaryPath: bin})
	th := c.StartThread(ThreadOptions{})

	// First run — script echoes args; we just want to capture the SDK's
	// auto-set ID. The fake echoes thread.started with the id "echoed-id".
	s1, err := th.RunStreamed(context.Background(), StringInput("first"), TurnOptions{})
	if err != nil { t.Fatal(err) }
	for range s1.Events() {}
	if err := s1.Wait(); err != nil { t.Fatal(err) }
	if th.ID() != "echoed-id" {
		t.Fatalf("first-run ID = %q, want echoed-id", th.ID())
	}

	// Second run — args should now include resume <id>.
	s2, err := th.RunStreamed(context.Background(), StringInput("second"), TurnOptions{})
	if err != nil { t.Fatal(err) }
	var msg2 string
	for evt := range s2.Events() {
		if ic, ok := evt.(*ItemCompletedEvent); ok {
			if am, ok := ic.Item.(*AgentMessageItem); ok {
				msg2 = am.Text
			}
		}
	}
	if err := s2.Wait(); err != nil { t.Fatal(err) }

	if !contains(msg2, "resume echoed-id") {
		t.Errorf("2nd-run args = %q, missing resume", msg2)
	}
}

// writeArgEchoScript creates a one-off bash fake that emits its argv as an
// agent_message item, plus a thread.started with id "echoed-id".
func writeArgEchoScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "echo.sh")
	body := `#!/bin/bash
cat > /dev/null
echo '{"type":"thread.started","thread_id":"echoed-id"}'
ARGS_JSON=$(printf '%s ' "$@" | sed 's/"/\\"/g')
echo "{\"type\":\"item.completed\",\"item\":{\"id\":\"i1\",\"type\":\"agent_message\",\"text\":\"args: ${ARGS_JSON}\"}}"
echo '{"type":"turn.completed","usage":{"input_tokens":0,"cached_input_tokens":0,"output_tokens":0,"reasoning_output_tokens":0}}'
exit 0
`
	if err := os.WriteFile(bin, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func contains(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
```

Add the required imports at top of `thread_test.go`:
```go
import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run 'StartThread|ResumeThread'`
Expected: FAIL undefined Codex / Thread / etc.

- [ ] **Step 3: Implement codex.go**

`codex.go`:
```go
package codex

// CodexOptions mirrors TS `CodexOptions`. See spec §"Public API" for the
// full TS-to-Go field mapping.
type CodexOptions struct {
	BinaryPath string
	BaseURL    string
	APIKey     string
	Config     map[string]any
	Env        map[string]string
}

// Codex is the top-level handle. Cheap to construct; safe to share across
// goroutines (state lives on Thread, not Codex).
type Codex struct {
	opts CodexOptions
}

// New creates a Codex. BinaryPath defaults to "codex" (PATH lookup).
func New(opts CodexOptions) *Codex {
	if opts.BinaryPath == "" {
		opts.BinaryPath = "codex"
	}
	return &Codex{opts: opts}
}

// StartThread builds a Thread with no thread_id. The first RunStreamed
// captures the codex-generated id from `thread.started`.
func (c *Codex) StartThread(topts ThreadOptions) *Thread {
	return &Thread{codex: c, topts: topts}
}

// ResumeThread builds a Thread already bound to threadID. Every
// RunStreamed appends `resume <threadID>` to the codex invocation.
func (c *Codex) ResumeThread(threadID string, topts ThreadOptions) *Thread {
	return &Thread{codex: c, topts: topts, id: threadID}
}
```

- [ ] **Step 4: Implement thread.go (RunStreamed only — Run added in Task 14)**

`thread.go`:
```go
package codex

import (
	"context"
	"os"
	"sync"
)

// Thread mirrors TS `Thread`. NOT safe for concurrent Run / RunStreamed
// — codex CLI is one-prompt-per-process; serialize at the caller level.
type Thread struct {
	codex *Codex
	topts ThreadOptions

	idMu sync.RWMutex
	id   string
}

// ID returns the thread_id, captured from the first thread.started event
// or set explicitly by ResumeThread. Returns "" before either has happened.
func (t *Thread) ID() string {
	t.idMu.RLock()
	defer t.idMu.RUnlock()
	return t.id
}

func (t *Thread) setID(id string) {
	t.idMu.Lock()
	t.id = id
	t.idMu.Unlock()
}

// StreamedTurn mirrors TS `RunStreamedResult`. Events() yields typed
// events in order; Wait() returns once the events channel is fully
// drained AND the codex subprocess has exited.
type StreamedTurn = stream

// RunStreamedResult is a TS-parity alias for StreamedTurn.
type RunStreamedResult = StreamedTurn

// RunStreamed sends the prompt and returns a stream of typed events.
func (t *Thread) RunStreamed(ctx context.Context, input Input, topts TurnOptions) (*StreamedTurn, error) {
	prompt, images := joinTextParts(input)

	schemaPath, cleanupSchema, err := prepareOutputSchema(topts.OutputSchema)
	if err != nil {
		return nil, err
	}

	args, err := buildArgs(buildArgsInput{
		CodexOpts:        t.codex.opts,
		ThreadOpts:       t.topts,
		ThreadID:         t.ID(),
		Images:           images,
		OutputSchemaPath: schemaPath,
	})
	if err != nil {
		cleanupSchema()
		return nil, err
	}

	env := composeEnv(t.codex.opts, os.Environ())

	s, err := runExec(ctx, runExecInput{
		Binary: t.codex.opts.BinaryPath,
		Args:   args,
		Env:    env,
		Prompt: prompt,
	})
	if err != nil {
		cleanupSchema()
		return nil, err
	}

	// Wrap the stream so cleanupSchema fires when the consumer reads
	// terminal status, AND so we can intercept thread.started to populate
	// Thread.id BEFORE the consumer sees the event.
	wrapped := wrapStream(s, t, cleanupSchema)
	return wrapped, nil
}

// wrapStream tees the inner stream's events through a goroutine that
// intercepts ThreadStartedEvent (to set Thread.id atomically before
// forwarding) and ensures cleanupSchema runs after Wait completes.
func wrapStream(inner *stream, t *Thread, cleanupSchema func()) *stream {
	out := &stream{
		events:   make(chan ThreadEvent, 16),
		waitDone: make(chan struct{}),
	}
	go func() {
		// LIFO: events must close before waitDone (see runExec for rationale).
		defer close(out.waitDone)
		defer close(out.events)
		for evt := range inner.Events() {
			if started, ok := evt.(*ThreadStartedEvent); ok {
				t.setID(started.ThreadID)
			}
			out.events <- evt
		}
		out.terminalErr = inner.Wait()
		cleanupSchema()
	}()
	return out
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./... -run 'StartThread|ResumeThread' -v -timeout=30s`
Expected: all 3 tests PASS.

- [ ] **Step 6: Commit**

```bash
git add codex.go thread.go thread_test.go
git commit -m "feat: Codex + Thread.RunStreamed + thread.started id capture"
```

---

## Task 14: Thread.Run + Turn aggregation

**Files:**
- Modify: `thread.go` (add `Run`, `Turn`)
- Create: `thread_run_test.go`

- [ ] **Step 1: Write the failing test**

`thread_run_test.go`:
```go
package codex

import (
	"context"
	"errors"
	"testing"
)

func TestRun_BuffersUntilCompletion(t *testing.T) {
	c := fakeCodex(t, "clean.sh")
	th := c.StartThread(ThreadOptions{})
	turn, err := th.Run(context.Background(), StringInput("hi"), TurnOptions{})
	if err != nil { t.Fatal(err) }
	if turn.FinalResponse != "hello" {
		t.Errorf("FinalResponse = %q", turn.FinalResponse)
	}
	if len(turn.Items) != 1 {
		t.Errorf("Items = %d, want 1", len(turn.Items))
	}
	if turn.Usage == nil || turn.Usage.OutputTokens != 2 {
		t.Errorf("Usage = %+v", turn.Usage)
	}
	if th.ID() != "01HMFAKE" {
		t.Errorf("ID = %q", th.ID())
	}
}

func TestRun_TurnFailedReturnsTurnFailedError(t *testing.T) {
	c := fakeCodex(t, "turn_failed.sh")
	th := c.StartThread(ThreadOptions{})
	_, err := th.Run(context.Background(), StringInput("hi"), TurnOptions{})
	if err == nil { t.Fatal("expected error") }
	var tf *TurnFailedError
	if !errors.As(err, &tf) {
		t.Fatalf("err = %T (%v), want *TurnFailedError", err, err)
	}
	if tf.Message != "model rejected" {
		t.Errorf("Message = %q", tf.Message)
	}
}

func TestRun_NonZeroExitReturnsExitError(t *testing.T) {
	c := fakeCodex(t, "exit_nonzero.sh")
	th := c.StartThread(ThreadOptions{})
	_, err := th.Run(context.Background(), StringInput("hi"), TurnOptions{})
	if err == nil { t.Fatal("expected error") }
	var nz *NonZeroExitError
	if !errors.As(err, &nz) {
		t.Fatalf("err = %T (%v), want *NonZeroExitError", err, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./... -run '^TestRun_'`
Expected: FAIL undefined `Run`.

- [ ] **Step 3: Append Run + Turn to thread.go**

Append to `thread.go`:
```go
// Turn mirrors TS `RunResult`. Aggregated state from a buffered Run().
type Turn struct {
	Items         []ThreadItem
	FinalResponse string
	Usage         *Usage
}

// RunResult is a TS-parity alias for Turn.
type RunResult = Turn

// Run is the buffered convenience wrapper around RunStreamed. Mirrors TS
// `Thread.run` (thread.ts:115-138):
//
//   - item.completed → appended to Items; AgentMessageItem.Text overwrites FinalResponse
//   - turn.completed → Usage set
//   - turn.failed   → returns (zero Turn, *TurnFailedError); channel still drained
//   - subprocess errors (Spawn/NonZeroExit/ctx) → returned as-is from Wait()
func (t *Thread) Run(ctx context.Context, input Input, topts TurnOptions) (Turn, error) {
	stream, err := t.RunStreamed(ctx, input, topts)
	if err != nil { return Turn{}, err }

	var turn Turn
	var failed *TurnFailedError
	for evt := range stream.Events() {
		switch e := evt.(type) {
		case *ItemCompletedEvent:
			turn.Items = append(turn.Items, e.Item)
			if am, ok := e.Item.(*AgentMessageItem); ok {
				turn.FinalResponse = am.Text
			}
		case *TurnCompletedEvent:
			u := e.Usage
			turn.Usage = &u
		case *TurnFailedEvent:
			msg := e.Error.Message
			failed = &TurnFailedError{Message: msg}
		}
	}

	if werr := stream.Wait(); werr != nil {
		return Turn{}, werr
	}
	if failed != nil {
		return Turn{}, failed
	}
	return turn, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./... -run '^TestRun_' -v -timeout=30s`
Expected: all 3 tests PASS.

- [ ] **Step 5: Run full suite to confirm no regression**

Run: `go test ./... -v -timeout=60s`
Expected: every test from Tasks 2-14 passes.

- [ ] **Step 6: Commit**

```bash
git add thread.go thread_run_test.go
git commit -m "feat: Thread.Run buffered wrapper + Turn aggregation"
```

---

## Task 15: Wire-parity fixture test

**Files:**
- Create: `wire_parity_test.go`
- Create: `testdata/fake_codex/spy.sh`

**Goal:** Capture the actual argv + env + stdin the SDK passes to codex,
and assert it matches a canonical trace. This catches drift from the spec's
"15-step argv order" that pure unit tests on `buildArgs` could miss.

- [ ] **Step 1: Create the spy script**

`testdata/fake_codex/spy.sh`:
```bash
#!/bin/bash
# Records argv, env, and stdin to files in $SPY_OUT, then emits a clean
# turn so the SDK is happy.
set -u
out="${SPY_OUT:?SPY_OUT must be set by the test}"
printf '%s\n' "$@" > "$out/argv"
env | sort > "$out/env"
cat > "$out/stdin"
echo '{"type":"thread.started","thread_id":"spy-id"}'
echo '{"type":"turn.completed","usage":{"input_tokens":0,"cached_input_tokens":0,"output_tokens":0,"reasoning_output_tokens":0}}'
exit 0
```
Make executable: `chmod +x testdata/fake_codex/spy.sh`

- [ ] **Step 2: Write the failing test**

`wire_parity_test.go`:
```go
package codex

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func runSpy(t *testing.T, c *Codex, th *Thread, input Input, opts TurnOptions) (argv []string, env map[string]string, stdin string) {
	t.Helper()
	out := t.TempDir()
	t.Setenv("SPY_OUT", out)

	stream, err := th.RunStreamed(context.Background(), input, opts)
	if err != nil { t.Fatal(err) }
	for range stream.Events() {}
	if err := stream.Wait(); err != nil { t.Fatal(err) }

	argvBytes, err := os.ReadFile(filepath.Join(out, "argv"))
	if err != nil { t.Fatal(err) }
	envBytes, err := os.ReadFile(filepath.Join(out, "env"))
	if err != nil { t.Fatal(err) }
	stdinBytes, err := os.ReadFile(filepath.Join(out, "stdin"))
	if err != nil { t.Fatal(err) }

	argv = strings.Split(strings.TrimRight(string(argvBytes), "\n"), "\n")
	env = map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(string(envBytes), "\n"), "\n") {
		if i := strings.IndexByte(line, '='); i >= 0 {
			env[line[:i]] = line[i+1:]
		}
	}
	stdin = string(stdinBytes)
	return
}

func spyCodex(t *testing.T, opts CodexOptions) *Codex {
	_, thisFile, _, _ := runtime.Caller(0)
	opts.BinaryPath = filepath.Join(filepath.Dir(thisFile), "testdata", "fake_codex", "spy.sh")
	return New(opts)
}

func TestWireParity_StartThread_MinimalArgs(t *testing.T) {
	c := spyCodex(t, CodexOptions{APIKey: "sk-test"})
	th := c.StartThread(ThreadOptions{})
	argv, env, stdin := runSpy(t, c, th, StringInput("hello"), TurnOptions{})

	wantArgv := []string{"exec", "--experimental-json"}
	if !reflect.DeepEqual(argv, wantArgv) {
		t.Errorf("argv = %v, want %v", argv, wantArgv)
	}
	if env["CODEX_API_KEY"] != "sk-test" {
		t.Errorf("CODEX_API_KEY = %q", env["CODEX_API_KEY"])
	}
	if env["CODEX_INTERNAL_ORIGINATOR_OVERRIDE"] != "codex_sdk_go" {
		t.Errorf("originator = %q", env["CODEX_INTERNAL_ORIGINATOR_OVERRIDE"])
	}
	if stdin != "hello" {
		t.Errorf("stdin = %q, want %q", stdin, "hello")
	}
}

func TestWireParity_FullArgs_MatchesSpecOrdering(t *testing.T) {
	tt := true
	c := spyCodex(t, CodexOptions{
		BaseURL: "https://api.x.example/v1",
		APIKey:  "sk-x",
	})
	th := c.StartThread(ThreadOptions{
		Model:                "o3",
		SandboxMode:          SandboxWorkspaceWrite,
		WorkingDirectory:     "/tmp/w",
		AdditionalDirs:       []string{"/d1"},
		SkipGitRepoCheck:     true,
		ModelReasoningEffort: ReasoningHigh,
		NetworkAccessEnabled: &tt,
		WebSearchMode:        WebSearchLive,
		ApprovalPolicy:       ApprovalOnRequest,
	})
	argv, _, _ := runSpy(t, c, th, PartsInput{
		{Type: InputText, Text: "hello"},
		{Type: InputLocalImage, Path: "/x.png"},
	}, TurnOptions{})

	want := []string{
		"exec", "--experimental-json",
		"--config", `openai_base_url="https://api.x.example/v1"`,
		"--model", "o3",
		"--sandbox", "workspace-write",
		"--cd", "/tmp/w",
		"--add-dir", "/d1",
		"--skip-git-repo-check",
		"--config", `model_reasoning_effort="high"`,
		"--config", "sandbox_workspace_write.network_access=true",
		"--config", `web_search="live"`,
		"--config", `approval_policy="on-request"`,
		"--image", "/x.png",
	}
	if !reflect.DeepEqual(argv, want) {
		t.Errorf("\ngot:  %v\nwant: %v", argv, want)
	}
}

func TestWireParity_ResumeThread_ImageAfterResume(t *testing.T) {
	c := spyCodex(t, CodexOptions{})
	th := c.ResumeThread("thread-7", ThreadOptions{})
	argv, _, _ := runSpy(t, c, th, PartsInput{
		{Type: InputText, Text: "continue"},
		{Type: InputLocalImage, Path: "/p.png"},
	}, TurnOptions{})

	want := []string{
		"exec", "--experimental-json",
		"resume", "thread-7",
		"--image", "/p.png",
	}
	if !reflect.DeepEqual(argv, want) {
		t.Errorf("\ngot:  %v\nwant: %v", argv, want)
	}
}
```

- [ ] **Step 3: Run test to verify it fails initially OR passes immediately**

Run: `go test ./... -run 'WireParity' -v`
Expected: PASS if implementation is correct (this test exists to lock
behavior — if it fails, fix `buildArgs` to match). If failing, the diff
will show the exact wire deviation.

- [ ] **Step 4: Commit**

```bash
git add wire_parity_test.go testdata/fake_codex/spy.sh
git commit -m "test: wire-parity spy fixture covering argv/env/stdin trace"
```

---

## Task 16: Integration test (gated by build tag)

**Files:**
- Create: `integration_test.go`

- [ ] **Step 1: Write the gated test**

`integration_test.go`:
```go
//go:build integration

package codex

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Run with: go test -tags=integration ./... -timeout=2m
//
// Requires: codex binary on PATH; OPENAI_API_KEY env set.
// Skipped automatically when those preconditions don't hold.

func TestIntegration_Quickstart(t *testing.T) {
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set")
	}

	c := New(CodexOptions{APIKey: os.Getenv("OPENAI_API_KEY")})
	cwd, err := os.MkdirTemp("", "codex-itest-")
	if err != nil { t.Fatal(err) }
	defer os.RemoveAll(cwd)

	th := c.StartThread(ThreadOptions{
		SandboxMode:      SandboxReadOnly,
		WorkingDirectory: cwd,
		SkipGitRepoCheck: true,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	turn, err := th.Run(ctx, StringInput("Reply with the single word: pong"), TurnOptions{})
	if err != nil { t.Fatalf("Run: %v", err) }

	if !strings.Contains(strings.ToLower(turn.FinalResponse), "pong") {
		t.Errorf("FinalResponse = %q, expected to contain 'pong'", turn.FinalResponse)
	}
	if th.ID() == "" {
		t.Errorf("ID() empty after run")
	}
	if turn.Usage == nil || turn.Usage.OutputTokens == 0 {
		t.Errorf("Usage looks empty: %+v", turn.Usage)
	}
}
```

- [ ] **Step 2: Verify it builds under the integration tag**

Run: `go vet -tags=integration ./...`
Expected: no errors (file compiles).

- [ ] **Step 3: (Optional, manual) Run against live codex**

If you have the codex binary on PATH and a working `OPENAI_API_KEY`:
```bash
go test -tags=integration ./... -run Integration -v -timeout=2m
```
Expected: PASS, asserts the FinalResponse contains "pong".

If you don't have credentials, skip this — the file is gated and won't
run in normal `go test ./...`.

- [ ] **Step 4: Commit**

```bash
git add integration_test.go
git commit -m "test: gated integration test against real codex binary"
```

---

## Task 17: Quickstart example + README polish

**Files:**
- Create: `examples/quickstart/main.go`
- Modify: `README.md`

- [ ] **Step 1: Write the quickstart example**

`examples/quickstart/main.go`:
```go
package main

import (
	"context"
	"fmt"
	"os"

	codex "github.com/agentserver/codex-agent-sdk-go"
)

func main() {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "OPENAI_API_KEY not set")
		os.Exit(1)
	}

	c := codex.New(codex.CodexOptions{APIKey: apiKey})

	cwd, err := os.MkdirTemp("", "codex-quickstart-")
	if err != nil { panic(err) }
	defer os.RemoveAll(cwd)

	th := c.StartThread(codex.ThreadOptions{
		SandboxMode:      codex.SandboxReadOnly,
		WorkingDirectory: cwd,
		SkipGitRepoCheck: true,
	})

	stream, err := th.RunStreamed(context.Background(),
		codex.StringInput("Tell me a one-line haiku about Go."),
		codex.TurnOptions{})
	if err != nil { panic(err) }

	for evt := range stream.Events() {
		switch e := evt.(type) {
		case *codex.ItemCompletedEvent:
			if am, ok := e.Item.(*codex.AgentMessageItem); ok {
				fmt.Println("AGENT:", am.Text)
			}
		case *codex.TurnCompletedEvent:
			fmt.Printf("USAGE: in=%d out=%d\n", e.Usage.InputTokens, e.Usage.OutputTokens)
		}
	}
	if err := stream.Wait(); err != nil {
		fmt.Fprintln(os.Stderr, "stream error:", err)
		os.Exit(1)
	}
	fmt.Println("THREAD ID:", th.ID())
}
```

- [ ] **Step 2: Verify the example compiles**

Run: `cd /root/codex-agent-sdk-go && go build ./examples/...`
Expected: produces `examples/quickstart/quickstart` binary, no errors.

- [ ] **Step 3: Polish README.md**

Append the following sections to `README.md` after the existing
Quickstart:

```markdown
## Streaming events

```go
stream, _ := th.RunStreamed(ctx, codex.StringInput("..."), codex.TurnOptions{})
for evt := range stream.Events() {
    switch e := evt.(type) {
    case *codex.ItemCompletedEvent:
        if am, ok := e.Item.(*codex.AgentMessageItem); ok {
            fmt.Println(am.Text)
        }
    case *codex.TurnFailedEvent:
        fmt.Println("turn failed:", e.Error.Message)
    }
}
if err := stream.Wait(); err != nil {
    log.Fatal(err)
}
```

## Resume

```go
// Pre-existing thread (from a prior session):
th := c.ResumeThread("01HMTHREAD...", codex.ThreadOptions{})

// Or capture id from a fresh thread:
th := c.StartThread(codex.ThreadOptions{})
turn, _ := th.Run(ctx, codex.StringInput("..."), codex.TurnOptions{})
fmt.Println(th.ID()) // populated after first turn
turn2, _ := th.Run(ctx, codex.StringInput("continue"), codex.TurnOptions{})
// turn2 implicitly resumes — the SDK appends `resume <id>` automatically.
```

## Errors

- `*codex.SpawnError` — codex binary couldn't start (PATH, perms)
- `*codex.NonZeroExitError` — codex exited non-zero or by signal
- `*codex.ParseEventError` — JSONL line failed to parse (also surfaces as
  a synthetic `ThreadErrorEvent` on the channel)
- `*codex.TurnFailedError` — `Run()` only; `RunStreamed` yields the
  `TurnFailedEvent` instead

## Alignment with `@openai/codex-sdk` (TypeScript)

This SDK is a port of the official TypeScript SDK. Every option, default,
env var, and CLI argument is reproduced. Intentional divergences:

1. **Binary discovery** — PATH lookup only (no npm platform-package
   fallback). Override via `CodexOptions.BinaryPath`.
2. **Cancellation** — `ctx.Done` triggers SIGTERM with 2s grace, then
   SIGKILL. TS uses single SIGTERM.
3. **Originator** — `CODEX_INTERNAL_ORIGINATOR_OVERRIDE` defaults to
   `"codex_sdk_go"`.
4. **Concurrency** — `Thread.Run` / `RunStreamed` are documented as
   not safe for concurrent calls (TS doesn't formalize this).

See full design at
`docs/superpowers/specs/2026-05-04-codex-agent-sdk-go-design.md` in the
agentserver repo.
```

- [ ] **Step 4: Verify everything still passes**

Run: `cd /root/codex-agent-sdk-go && go test ./... -v -timeout=60s`
Expected: every test passes.

Run: `cd /root/codex-agent-sdk-go && go vet ./...`
Expected: no issues.

- [ ] **Step 5: Commit**

```bash
git add examples/quickstart/main.go README.md
git commit -m "docs: quickstart example + README polish"
```

---

## Self-Review Checklist (run after all 17 tasks)

- [ ] **Spec coverage:** every section of
  `docs/superpowers/specs/2026-05-04-codex-agent-sdk-go-design.md` traces
  to at least one task. The Divergences table (4 items) is reflected in
  `README.md` and locked by tests (PATH lookup = absence of npm fallback,
  SIGTERM escalation = `TestRunExec_CtxCancelEscalatesToKill`,
  originator = wire parity test, concurrency note = README + doc comment).

- [ ] **Wire parity:** every argv-related field on `ThreadOptions` /
  `CodexOptions` / `TurnOptions` has at least one assertion in
  `wire_parity_test.go` OR `exec_args_test.go`.

- [ ] **No placeholders:** every step contains complete, copy-pasteable
  Go code; no "TBD", "implement later", or "similar to above".

- [ ] **TDD discipline:** every implementation task has a failing-test
  step before its implementation step.

- [ ] **Type consistency:** `Turn`, `StreamedTurn`, `ThreadEvent`,
  `ThreadItem`, `Input`, `Usage`, `ThreadError` are referenced
  consistently across tasks (no rename drift).

---

**Plan complete and saved to `docs/superpowers/plans/2026-05-04-codex-agent-sdk-go.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
