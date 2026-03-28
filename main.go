package main

import (
	"fmt"
	"tally/internal/pipeline"
)

func main() {
	fmt.Println("tally ready")
	pipeline.RunIngest()
}
