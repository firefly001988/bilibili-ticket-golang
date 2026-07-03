package global

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
)

// Fault is a structured error that captures the source location (file and line
// number) where the error was created. It is designed to be serialised as the
// `cause` field of Wails v3 CallError so that the frontend can display precise
// error locations.
//
// Fault implements json.Marshaler so that a custom Wails marshalError function
// can produce a structured JSON object instead of the default opaque error
// string.
type Fault struct {
	Op   string `json:"op"`    // operation that failed, e.g. "加载配置"
	File string `json:"file"`  // source file name, e.g. "main.go"
	Line int    `json:"line"`  // source line number
	Err  error  `json:"error"` // underlying error
	Hint string `json:"hint"`  // human-readable suggestion, e.g. "删除 data/store.bin 以重置配置"
}

// NewFault creates a Fault, automatically capturing the caller's file and line
// number via runtime.Caller(1). The op describes what operation failed, err is
// the underlying error (may be nil), and hint is a human-readable suggestion.
//
// Example:
//
//	global.NewFault("加载配置", err, "删除 data/store.bin 以重置配置")
func NewFault(op string, err error, hint string) *Fault {
	_, file, line, ok := runtime.Caller(1)
	if !ok {
		file = "???"
		line = 0
	}
	// Strip the full path down to just the file name.
	if idx := strings.LastIndexByte(file, '/'); idx >= 0 {
		file = file[idx+1:]
	} else if idx := strings.LastIndexByte(file, '\\'); idx >= 0 {
		file = file[idx+1:]
	}
	return &Fault{
		Op:   op,
		File: file,
		Line: line,
		Err:  err,
		Hint: hint,
	}
}

// NewFaultAt is like NewFault but accepts an explicit file and line number
// instead of using runtime.Caller. Useful when the error origin is known
// statically (e.g. in generated code or tests).
func NewFaultAt(op string, err error, hint string, file string, line int) *Fault {
	return &Fault{
		Op:   op,
		File: file,
		Line: line,
		Err:  err,
		Hint: hint,
	}
}

// Error formats the Fault as a human-readable single-line string suitable for
// logs and for the Wails CallError.Message field.
//
// Format: [file:line] op: underlying error — hint
func (f *Fault) Error() string {
	var b strings.Builder
	b.WriteByte('[')
	b.WriteString(f.File)
	b.WriteByte(':')
	fmt.Fprintf(&b, "%d", f.Line)
	b.WriteString("] ")
	b.WriteString(f.Op)
	if f.Err != nil {
		b.WriteString(": ")
		b.WriteString(f.Err.Error())
	}
	if f.Hint != "" {
		b.WriteString(" — 建议: ")
		b.WriteString(f.Hint)
	}
	return b.String()
}

// Unwrap returns the underlying error so that errors.Is and errors.As work
// correctly with Fault.
func (f *Fault) Unwrap() error {
	return f.Err
}

// MarshalJSON implements json.Marshaler. It produces a structured JSON object
// that Wails v3 will place into the CallError.Cause field, replacing the
// default opaque error serialisation.
func (f *Fault) MarshalJSON() ([]byte, error) {
	// We need to handle the Err field specially: if it's a *Fault itself, we
	// want its Error() string; if it's a plain error, use Error(); if nil,
	// omit.
	type faultJSON struct {
		Op    string `json:"op"`
		File  string `json:"file"`
		Line  int    `json:"line"`
		Error string `json:"error,omitempty"`
		Hint  string `json:"hint,omitempty"`
	}
	fj := faultJSON{
		Op:   f.Op,
		File: f.File,
		Line: f.Line,
		Hint: f.Hint,
	}
	if f.Err != nil {
		fj.Error = f.Err.Error()
	}
	return json.Marshal(fj)
}

// FaultFrom extracts and returns the first *Fault found by walking the error
// chain via errors.As / Unwrap. Returns nil if no Fault is in the chain.
func FaultFrom(err error) *Fault {
	if err == nil {
		return nil
	}
	// Walk the chain manually to avoid importing errors (keeps this package
	// dependency-free).
	type unwrapper interface{ Unwrap() error }
	for {
		if f, ok := err.(*Fault); ok {
			return f
		}
		u, ok := err.(unwrapper)
		if !ok {
			return nil
		}
		err = u.Unwrap()
		if err == nil {
			return nil
		}
	}
}

// MarshalError is a Wails-compatible error marshalling function. It produces
// JSON for the CallError.Cause field. If the error chain contains a *Fault,
// that Fault's MarshalJSON is used; otherwise the error is serialised as a
// plain string.
//
// Usage with Wails v3:
//
//	app := application.New(application.Options{
//	    Bindings: application.NewBindings(global.MarshalError, nil),
//	    ...
//	})
func MarshalError(err error) []byte {
	if err == nil {
		return []byte("null")
	}
	if f := FaultFrom(err); f != nil {
		data, marshalErr := f.MarshalJSON()
		if marshalErr == nil {
			return data
		}
	}
	// Fallback: wrap the error message in a minimal JSON structure so the
	// frontend always receives an object.
	data, _ := json.Marshal(struct {
		Error string `json:"error"`
	}{Error: err.Error()})
	return data
}
