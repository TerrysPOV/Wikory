package app

import (
	"encoding/json"
	"fmt"
	"io"
)

// SchemaVersion is the --json contract version. Bump it only with a breaking change.
const SchemaVersion = "v1"

// Envelope is the stable shape of every --json output.
type Envelope struct {
	Schema  string   `json:"schema"`
	OK      bool     `json:"ok"`
	Data    any      `json:"data,omitempty"`
	Error   *ErrBody `json:"error,omitempty"`
	Notices []string `json:"notices,omitempty"`
}

// ErrBody describes a failure in machine-readable form.
type ErrBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func schemaFor(cmd string) string { return "wikory." + cmd + "/" + SchemaVersion }

func writeEnvelope(w io.Writer, e Envelope) {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(e)
}

func writeNotices(w io.Writer, notices []string) {
	for _, n := range notices {
		fmt.Fprintf(w, "note: %s\n", n)
	}
}
