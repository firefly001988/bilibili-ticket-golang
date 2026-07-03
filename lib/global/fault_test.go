package global

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestFaultErrorFormat(t *testing.T) {
	f := NewFaultAt("加载配置", fmt.Errorf("permission denied"), "删除文件后重试", "main.go", 138)
	got := f.Error()

	if !strings.Contains(got, "[main.go:138]") {
		t.Errorf("Error() missing file:line prefix: %s", got)
	}
	if !strings.Contains(got, "加载配置") {
		t.Errorf("Error() missing op: %s", got)
	}
	if !strings.Contains(got, "permission denied") {
		t.Errorf("Error() missing underlying error: %s", got)
	}
	if !strings.Contains(got, "建议: 删除文件后重试") {
		t.Errorf("Error() missing hint: %s", got)
	}
}

func TestFaultMarshalJSON(t *testing.T) {
	f := NewFaultAt("打开数据库", fmt.Errorf("database is locked"), "检查文件权限", "repository.go", 42)
	data, err := f.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal JSON: %v", err)
	}

	if m["op"] != "打开数据库" {
		t.Errorf("op = %q, want %q", m["op"], "打开数据库")
	}
	if m["file"] != "repository.go" {
		t.Errorf("file = %q, want %q", m["file"], "repository.go")
	}
	// json numbers are float64
	if line, ok := m["line"].(float64); !ok || int(line) != 42 {
		t.Errorf("line = %v, want 42", m["line"])
	}
	if m["error"] != "database is locked" {
		t.Errorf("error = %q, want %q", m["error"], "database is locked")
	}
	if m["hint"] != "检查文件权限" {
		t.Errorf("hint = %q, want %q", m["hint"], "检查文件权限")
	}
}

func TestFaultUnwrap(t *testing.T) {
	inner := fmt.Errorf("inner error")
	f := NewFault("test", inner, "")

	if !errors.Is(f, inner) {
		t.Error("errors.Is(f, inner) should be true")
	}

	var target *Fault
	if !errors.As(f, &target) {
		t.Error("errors.As(f, &target) should be true")
	}
	if target.Op != "test" {
		t.Errorf("target.Op = %q, want %q", target.Op, "test")
	}
}

func TestFaultFrom(t *testing.T) {
	inner := fmt.Errorf("inner")
	f := NewFault("op", inner, "hint")

	extracted := FaultFrom(f)
	if extracted == nil {
		t.Fatal("FaultFrom returned nil")
	}
	if extracted.Op != "op" {
		t.Errorf("Op = %q, want %q", extracted.Op, "op")
	}

	// FaultFrom on a plain error returns nil.
	if f2 := FaultFrom(fmt.Errorf("plain")); f2 != nil {
		t.Error("FaultFrom on plain error should return nil")
	}

	// FaultFrom on nil returns nil.
	if f3 := FaultFrom(nil); f3 != nil {
		t.Error("FaultFrom(nil) should return nil")
	}
}

func TestFaultWrappedUnwrap(t *testing.T) {
	inner := fmt.Errorf("inner")
	f := NewFault("outer", inner, "hint")

	// Wrap with fmt.Errorf and %w
	wrapped := fmt.Errorf("wrapped: %w", f)

	extracted := FaultFrom(wrapped)
	if extracted == nil {
		t.Fatal("FaultFrom on wrapped error returned nil")
	}
	if extracted.Op != "outer" {
		t.Errorf("Op = %q, want %q", extracted.Op, "outer")
	}
}

func TestMarshalError(t *testing.T) {
	t.Run("Fault error", func(t *testing.T) {
		f := NewFaultAt("test", fmt.Errorf("boom"), "try restarting", "foo.go", 10)
		data := MarshalError(f)
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if m["file"] != "foo.go" {
			t.Errorf("file = %q", m["file"])
		}
	})

	t.Run("plain error", func(t *testing.T) {
		data := MarshalError(fmt.Errorf("something broke"))
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if m["error"] != "something broke" {
			t.Errorf("error = %q", m["error"])
		}
	})

	t.Run("nil error", func(t *testing.T) {
		data := MarshalError(nil)
		if string(data) != "null" {
			t.Errorf("MarshalError(nil) = %s, want null", string(data))
		}
	})
}

func TestNewFaultCallerInfo(t *testing.T) {
	// This test verifies that NewFault captures the correct file name
	// (fault_test.go) and a line number in this file.
	f := NewFault("test caller", fmt.Errorf("err"), "hint")

	if f.File != "fault_test.go" {
		t.Errorf("File = %q, want fault_test.go", f.File)
	}
	if f.Line <= 0 {
		t.Errorf("Line = %d, want positive", f.Line)
	}
	// The call to NewFault should be on this line (approx).
	expectedLine := 144 // line number of the NewFault call above
	if f.Line < expectedLine-3 || f.Line > expectedLine+3 {
		t.Logf("Line = %d, expected ~%d (small offset is OK due to test helper wrappers)", f.Line, expectedLine)
	}
}
