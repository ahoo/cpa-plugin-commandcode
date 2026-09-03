package plugin

import (
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// gjsonGetString reads a top-level string field without failing on bad JSON.
func gjsonGetString(body []byte, path string) string {
	if len(body) == 0 || !gjson.ValidBytes(body) {
		return ""
	}
	v := gjson.GetBytes(body, path)
	if v.Type != gjson.String {
		return ""
	}
	return v.String()
}

func sjsonSetString(body []byte, path, value string) ([]byte, error) {
	return sjson.SetBytes(body, path, value)
}
