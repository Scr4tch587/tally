package pipeline

import "tally/internal/event"
import "context"
import "sync"
import "fmt"
import "time"

func ingest(ctx context.Context, raw <-chan string) <-chan *event.CanonicalEvent {
	out := make(chan *event.CanonicalEvent, 10)
	go func() {
		defer close(out)
		n := &event.LedgerNormalizer{}
		for s := range raw {
			ce, err := n.Normalize([]byte(s))
			if err == nil {
				out <- ce
			}
		}
	}()
	return out
}

func match (ctx context.Context, events <-chan *event.CanonicalEvent) <-chan struct{} {
	matcher := make(map[int64]*event.CanonicalEvent)
	out := make(chan struct{})
	var mu sync.Mutex
	go func() {
		defer close(out)
		for ce := range events {
			mu.Lock()
			v, ok := matcher[ce.AmountMinor]
			if ok {
				fmt.Printf("Match ID 1: %s\n", ce.EventID)
				fmt.Printf("Match ID 2: %s\n", v.EventID)
				out <- struct{}{}
				delete(matcher, ce.AmountMinor)
			} else {
				matcher[ce.AmountMinor] = ce
			}
			mu.Unlock()
		}
	}()
	return out
} 

func Report (ctx context.Context, signals <-chan struct{}) {
	counter := 0
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case _, ok := <-signals:
			if !ok {
				signals = nil
				continue
			}
			counter++
		case <-ticker.C:
			fmt.Printf("Matches found: %d\n", counter)
		case <- ctx.Done():
			return
		}
	}
}

func RunPipeline() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ch_raw := make(chan string, 20)
	go func() {
		defer close(ch_raw)
		for i := 1; i <= 200; i++ {
			amount := 100
			if i % 2 == 0 {
				amount = 200
			}
			ch_raw <- fmt.Sprintf(`{"EventID":"%d","SourceType":"ledger","AmountMinor":%d,"Currency":"USD"}`, i, amount)
		}
	}()

	Report(ctx, match(ctx, ingest(ctx, ch_raw)))
}