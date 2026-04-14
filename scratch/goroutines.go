//go:build ignore
// +build ignore

package main

import "fmt"
import "sync"
import "time"
import "context"

func main() {
	ch := make(chan int, 10)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i:= 1; i <= 1000; i++ {
			select {
			case ch <- i:
				time.Sleep(10 * time.Millisecond)
			case <-ctx.Done():
				close(ch)
				return
			}
		}
	}()

	go func() {
		defer wg.Done()
		for v := range ch {
			fmt.Println(v*v)
		}
	}()

	wg.Wait()
}