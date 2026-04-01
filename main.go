package main

import (
	"fmt"
	"goprint/api"
	"goprint/config"
	"log"
	"os"
)

func main() {
	fmt.Println("GoPrint is starting...")

	configPath := "config.yaml"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	cfg, err := config.LoadFromFile(configPath)
	if err != nil {
		log.Fatalf("Failed to load config %s: %v", configPath, err)
	}
	api.SetConfig(cfg)

	router := api.SetupRouter()

	port := fmt.Sprintf(":%d", cfg.Server.Port)
	fmt.Printf("Running on http://localhost%s\n", port)

	if err := router.Run(port); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
