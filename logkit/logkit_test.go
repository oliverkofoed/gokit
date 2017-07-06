package logkit

import (
	"context"
	"testing"
	"time"
)

/*

	logkit.Debug(ctx, "blfdjaklfdjlkdfsjkafjkldfjafds klfdsj aklfd ", args...)
	logkit.Debugf(ctx, "blfdjaklfdjlkdfsjkafjkldfjafds klfdsj aklfd ", args...)
	logkit.Info(ctx, "blfdjaklfdjlkdfsjkafjkldfjafds klfdsj aklfd ", args...)
	logkit.Infof(ctx, "blfdjaklfdjlkdfsjkafjkldfjafds klfdsj aklfd ", args...)
	logkit.Warn(ctx, "blfdjaklfdjlkdfsjkafjkldfjafds klfdsj aklfd ", args...)
	logkit.Warnf(ctx, "blfdjaklfdjlkdfsjkafjkldfjafds klfdsj aklfd ", args...)
	logkit.Error(ctx, "blfdjaklfdjlkdfsjkafjkldfjafds klfdsj aklfd ", args...)
	logkit.Errorf(ctx, "blfdjaklfdjlkdfsjkafjkldfjafds klfdsj aklfd ", args...)

	ctx := logkit.context
	ctx.Info
	ctx.Infof


	ctx, done = logkit.Operation("bdasdas", args..)
	defer done()

*/
func TestMain(t *testing.T) {
	ctx := context.Background()

	Info(ctx, "starting")
	simulateWebRequest(ctx)
	Info(ctx, "line")
	simulateWebRequest(ctx)

	Info(ctx, "done")

	time.Sleep(time.Millisecond * 300)
}

// -------------------------------

// -------------------------------

func simulateWebRequest(ctx context.Context) {
	//DefaultOutput = NewOutputFilter(DefaultOutput, false, false, false, false, true, true)
	//log, done := Operation(ctx, "web.request")
	//defer done()

	//fmt.Println("---")
	log, done := OperationWithOutput(ctx, "web.request", NewBufferedOutput(DefaultOutput, func(events []Event) []Event {
		return events
	}))
	defer done()
	/*defer func() {
		for _, item := range buffer.buffered {
			switch item.itemType {
			case 1:
				//regularOutput.B

			}
		}
		fmt.Println(buffer.buffered)
		done()
		fmt.Println("here")
	}()*/
	/*log, done := OperationWithOutput(ctx, "web.request", func(messages []*Message) []*Message {

	})
	defer done()*/
	/*defer func() {
		for _, item := range buffer.buffered {
			switch item.itemType {
			case 1:
				//regularOutput.B

			}
		}
		fmt.Println(buffer.buffered)
		done()
		fmt.Println("here")
	}()*/

	//log.CaptureEverything()

	log.Info("hello")

	Info(log, "log syntax #1")
	//ctx.Info("log syntax #2")
	time.Sleep(time.Millisecond * 10)
	Info(log, "Finished")

	lookupUser(log)
	searchInventory(log)
	query(log)

	standardContextThing(log)
}

func standardContextThing(ctx context.Context) {
	log, done := context.WithCancel(ctx)
	defer done()

	Info(log, "standard ctx")

	op, d2 := Operation(log, "stdchild")
	defer d2()
	Info(op, "logkit ctx")
}

func lookupUser(ctx context.Context) {
	log, done := Operation(ctx, "lookup.user")
	defer done()
	log.Debug("Dewbugging.. Doing something else", String("url", "http://laaame.com"))
	log.Info("Doing so and so")
	log.Warn("I'm warning yoooo")
	log.Error("Erroring out")
}

func searchInventory(ctx context.Context) {
	log, done := Operation(ctx, "search.inventory")
	defer done()
	log.Info("It's something i'm working on.")
	log.Debug("Doing something else")
}

func query(ctx context.Context) {
	_, done := Operation(ctx, "cql.select", String("cql", "select * from something = blah"))
	defer done()
	time.Sleep(time.Millisecond * 10)
}
