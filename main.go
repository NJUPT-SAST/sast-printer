package main

import (
	"fmt"
	"goprint/api"
	"goprint/config"
	"log"
)

func main() {
	fmt.Println("GoPrint is starting...")

	cfg, err := config.LoadFromFile("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config.yaml: %v", err)
	}
	api.SetConfig(cfg)

	router := api.SetupRouter()

	port := fmt.Sprintf(":%d", cfg.Server.Port)
	fmt.Printf("Running on http://localhost%s\n", port)

	if err := router.Run(port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
