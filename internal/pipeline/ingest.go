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
			sources := []string{"ledger", "processor", "bank"}
			source := sources[(i-1)%len(sources)]
			ce, err := event.NewCanonicalEvent("tenant-dev", fmt.Sprintf("evt-%d", i), source, fmt.Sprintf("source-evt-%d", i), 100, "USD", "USD", time.Now(), "credit", "account-1", "counterparty-1", map[string]string{})
			if err != nil {
				fmt.Printf("error creating canonical event: %v\n", err)
				continue
			}
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
