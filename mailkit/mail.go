package mailkit

type Sender interface {
	Send(mail *Mail) error
}

type Mail struct {
	From     string
	To       []string
	CC       []string
	Subject  string
	BodyHTML string
	BodyText string
}
