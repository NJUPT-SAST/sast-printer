package main

import (
	"fmt"
	"goprint/api"
	"log"
)

func main() {
	fmt.Println("GoPrint is starting...")

	router := api.SetupRouter()

	port := ":5001"
	fmt.Printf("Running on http://localhost%s\n", port)

	if err := router.Run(port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
