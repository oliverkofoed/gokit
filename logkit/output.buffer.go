package logkit

type BufferedEventsFilter func([]Event) []Event

type outputBuffer struct {
	parent   Output
	buffered []Event
	filter   BufferedEventsFilter
}

func NewBufferedOutput(parent Output, filter BufferedEventsFilter) Output {
	return &outputBuffer{
		parent: parent,
		filter: filter,
	}
}

func (d *outputBuffer) Event(evt Event) {
	//TODO: needs a lock or other sync method
	d.buffered = append(d.buffered, evt)

	if evt.Type == EventTypeCompleteOperation && evt.Operation.Output == d && (evt.Operation.Parent == nil || evt.Operation.Parent.Output != d) {
		new := d.buffered
		if d.filter != nil {
			new = d.filter(d.buffered)
		}
		if d.buffered != nil && new != nil && len(new) > 0 {
			for _, e := range new {
				d.parent.Event(e)
			}
		}
	}
}
