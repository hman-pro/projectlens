package report

import (
	"encoding/json"
	"fmt"
	"io"
)

// JSONRenderer writes the Report as pretty-printed JSON.
type JSONRenderer struct {
	Indent string // defaults to two spaces
}

func (r JSONRenderer) Render(w io.Writer, rep *Report) error {
	enc := json.NewEncoder(w)
	indent := r.Indent
	if indent == "" {
		indent = "  "
	}
	enc.SetIndent("", indent)
	if err := enc.Encode(rep); err != nil {
		return fmt.Errorf("report: json render: %w", err)
	}
	return nil
}
