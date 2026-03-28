package config

import (
	"log"
	"os"
	"strconv"
)

// Config 应用配置
type Config struct {
	Server ServerConfig
	CUPS   CupsConfig
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Port int
	Host string
}

// CupsConfig CUPS配置
type CupsConfig struct {
	Host string
	Port int
}

// Load 加载配置
func Load() *Config {
	cfg := &Config{
		Server: ServerConfig{
			Port: getEnvInt("SERVER_PORT", 8080),
			Host: getEnvString("SERVER_HOST", "0.0.0.0"),
		},
		CUPS: CupsConfig{
			Host: getEnvString("CUPS_HOST", "localhost"),
			Port: getEnvInt("CUPS_PORT", 631),
		},
	}

	log.Println("Loaded configuration:")
	log.Printf("  Server: %s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("  CUPS: %s:%d", cfg.CUPS.Host, cfg.CUPS.Port)
	return cfg
}

// 辅助函数
func getEnvString(key, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value, exists := os.LookupEnv(key); exists {
		if intValue, err := strconv.Atoi(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}
