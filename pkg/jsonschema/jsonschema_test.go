package jsonschema

import (
	"encoding/json"
	"testing"
	"time"
)

type inner struct {
	Name string `json:"name"`
}

type sample struct {
	ID       string          `json:"id"`
	Count    int             `json:"count,omitempty"`
	Ratio    *float64        `json:"ratio"`
	Tags     []string        `json:"tags"`
	Meta     map[string]int  `json:"meta"`
	When     time.Time       `json:"when"`
	Raw      json.RawMessage `json:"raw"`
	Data     []byte          `json:"data"`
	Nested   inner           `json:"nested"`
	Items    []inner         `json:"items"`
	Skip     string          `json:"-"`
	Embedded                 // flattened
}

type Embedded struct {
	Extra bool `json:"extra"`
}

func TestOf(t *testing.T) {
	s := Of(sample{})
	props := s["properties"].(map[string]any)
	req := s["required"].([]string)
	if len(props) != 11 {
		t.Fatalf("props %v", props)
	}
	for _, r := range req {
		if r == "count" {
			t.Fatal("omitempty field required")
		}
	}
	check := func(name string, want string) {
		t.Helper()
		got, _ := json.Marshal(props[name])
		if string(got) != want {
			t.Errorf("%s: %s, want %s", name, got, want)
		}
	}
	check("id", `{"type":"string"}`)
	check("ratio", `{"type":["number","null"]}`)
	check("tags", `{"items":{"type":"string"},"type":"array"}`)
	check("meta", `{"additionalProperties":{"type":"integer"},"type":"object"}`)
	check("when", `{"format":"date-time","type":"string"}`)
	check("raw", `{}`)
	check("data", `{"contentEncoding":"base64","type":"string"}`)
	check("nested", `{"properties":{"name":{"type":"string"}},"required":["name"],"type":"object"}`)
	check("extra", `{"type":"boolean"}`)
	if _, ok := props["hidden"]; ok {
		t.Error("unexported field")
	}
	if Of(nil)["type"] != "object" {
		t.Error("nil")
	}
}
