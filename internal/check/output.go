package check

import (
	"encoding/json"
	"io"
)

func WriteJSON(w io.Writer, result Result) error {
	result.sortChecks()
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(result)
}
