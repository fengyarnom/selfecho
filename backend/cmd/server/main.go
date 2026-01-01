package main

import (
	"log"

	"selfecho/backend/internal/app"
)

func main() {
	if err := app.Run(); err != nil {
		log.Fatalf("server exited with error: %v", err)
	}
}
