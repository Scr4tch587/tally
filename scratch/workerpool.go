package main

import "fmt"
import "sync"

func main() {
	ch_tasks := make(chan int, 20)
	ch_results := make(chan int, 20)

	var wg sync.WaitGroup

	for i := 1; i <= 20; i++ {
		ch_tasks <- i
	}
	close(ch_tasks)

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range ch_tasks {
				ch_results <- task * task
			}
		}()
	}
	wg.Wait()
	close(ch_results)

	for i := range ch_results {
		fmt.Printf("%d\n", i)
	}
}