package pipeline

import "tally/internal/event"
import "fmt"
import "sync"
import "time"

func RunIngest() {
	ch := make(chan *event.CanonicalEvent, 10)
		
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		for i := 1; i <= 100; i++ {
			source := "payments"
			if i % 2 == 0 {
				source = "ledger"
			}
			ce, _ := event.NewCanonicalEvent(fmt.Sprintf("evt-%d", i), source, 100, "USD", time.Now())
			ch <- ce
		}
		close(ch)
	}()

	go func() {
		defer wg.Done()
		aggregator := make(map[string]int)
		for ce := range ch {
			aggregator[ce.SourceType] += 1
		}
		for k, v := range aggregator {
			fmt.Printf("consumer1: Key %s, Count %d\n", k, v)
		}
	}()

	go func() {
		defer wg.Done()
		aggregator := make(map[string]int)
		for ce := range ch {
			aggregator[ce.SourceType] += 1
		}
		for k, v := range aggregator {
			fmt.Printf("consumer2: Key %s, Count %d\n", k, v)
		}
	}()

	wg.Wait()
}