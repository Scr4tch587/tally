package main

import (
	"fmt"
	"tally/internal/store"
	"context"
	"time"
	"tally/internal/event"
)

func main() {
	fmt.Println("tally ready")
	ctx := context.Background()
	pool, err := store.Connect(ctx)
	if err != nil {
		fmt.Println("postgres connection failed: ", err)
		return
	} 
	defer pool.Close()

	err = pool.Ping(ctx)
	if err != nil {
		fmt.Println("postgres ping failed:", err)
		return
	}

	fmt.Println("postgres ok")

	ev1, err := event.NewCanonicalEvent("abc123", "stripe", 100, "USD", time.Now())
	if err != nil {
		fmt.Println("failed to create new event:", err)
		return
	}

	ev2, err := event.NewCanonicalEvent("abc124", "stripe", 101, "USD", time.Now())
	if err != nil {
		fmt.Println("failed to create new event:", err)
		return
	}

	err = store.InsertEvent(ctx, pool, ev1)
	if err != nil {
		fmt.Println("failed to insert event:", err)
		return
	}

	err = store.InsertEvent(ctx, pool, ev2)
	if err != nil {
		fmt.Println("failed to insert event:", err)
		return
	}

	err = store.ConfirmMatch(ctx, pool, ev1.EventID, ev2.EventID)
	if err != nil {
		fmt.Println("failed to confirm match:", err)
		return
	}

	err = store.ConfirmMatch(ctx, pool, ev1.EventID, ev2.EventID)
	if err != nil {
		fmt.Println("second match correctly rejected:", err)
	} else {
		fmt.Println("bug: second match should have failed")
	}
}
