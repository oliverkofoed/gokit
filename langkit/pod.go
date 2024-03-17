package langkit

import "encoding/json"

// Pod contains a string translated into multiple languages. It's ment to be serialized and stored on entities in databases, such as on "product description" or "title" columns.
type Pod map[string]string

func ParsePod(msg json.RawMessage) Pod {
	p := &Pod{}
	err := json.Unmarshal(msg, p)
	if err != nil {
		panic(err)
	}
	return *p
}

func CreatePod(def string) Pod {
	return Pod{"default": def}
}

func (t Pod) Default() string {
	return t.Get("default")
}

func (t Pod) Get(locale string) string {
	if v, found := t["default"]; found {
		return v
	}
	return "[no default]"
}

func (t Pod) JSON() json.RawMessage {
	b, err := json.Marshal(t)
	if err != nil {
		panic(err)
	}
	return json.RawMessage(b)
}
