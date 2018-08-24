package logkit

type SplitterOutput struct {
	targets []Output
}

func NewSplitterOutput(targets ...Output) Output {
	return &SplitterOutput{targets: targets}
}

func (d *SplitterOutput) Event(evt Event) {
	for _, target := range d.targets {
		target.Event(evt)
	}
}
