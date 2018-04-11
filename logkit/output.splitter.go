package logkit

type SplitterFilter struct {
	targets []Output
}

func NewSplitterFilter(targets ...Output) Output {
	return &SplitterFilter{targets: targets}
}

func (d *SplitterFilter) Event(evt Event) {
	for _, target := range d.targets {
		target.Event(evt)
	}
}
