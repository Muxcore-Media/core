package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	fmt.Println("MuxCore starting...")

	<-ctx.Done()
	fmt.Println("MuxCore shutting down.")
}
