package textkit

type StringWriter interface {
	Write(v []byte) (int, error)
	WriteString(value string) (int, error)
}
