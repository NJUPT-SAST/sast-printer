package config

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 应用配置
type Config struct {
	Server   ServerConfig    `yaml:"server"`
	Auth     AuthConfig      `yaml:"auth"`
	Printing PrintingConfig  `yaml:"printing"`
	Printers []PrinterConfig `yaml:"printers"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Port int    `yaml:"port"`
	Host string `yaml:"host"`
}

// AuthConfig 鉴权配置
type AuthConfig struct {
	Enabled bool             `yaml:"enabled"`
	Feishu  FeishuAuthConfig `yaml:"feishu"`
}

// FeishuAuthConfig 飞书 OAuth2 免登鉴权配置
type FeishuAuthConfig struct {
	AppID          string `yaml:"app_id"`
	AppSecret      string `yaml:"app_secret"`
	RedirectURI    string `yaml:"redirect_uri"`
	AuthorizeURL   string `yaml:"authorize_url"`
	TokenURL       string `yaml:"token_url"`
	UserInfoURL    string `yaml:"user_info_url"`
	RequestTimeout string `yaml:"request_timeout"`
	TokenCacheTTL  string `yaml:"token_cache_ttl"`
}

// PrintingConfig 打印相关全局配置
type PrintingConfig struct {
	IPPUsername         string `yaml:"ipp_username"`
	ManualDuplexHookTTL string `yaml:"manual_duplex_hook_ttl"`
}

// PrinterConfig 单台打印机配置
type PrinterConfig struct {
	ID                string `yaml:"id"`
	URI               string `yaml:"uri"`
	Visible           bool   `yaml:"visible"`
	Reverse           bool   `yaml:"reverse"`
	DuplexMode        string `yaml:"duplex_mode"`
	FirstPass         string `yaml:"first_pass"`
	PadToEven         *bool  `yaml:"pad_to_even"`
	ReverseFirstPass  bool   `yaml:"reverse_first_pass"`
	ReverseSecondPass bool   `yaml:"reverse_second_pass"`
	RotateSecondPass  bool   `yaml:"rotate_second_pass"`
	Note              string `yaml:"note"`
}

// LoadFromFile 从YAML文件加载配置
func LoadFromFile(path string) (*Config, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config yaml: %w", err)
	}

	applyDefaults(&cfg)

	if err := validateConfig(&cfg); err != nil {
		return nil, err
	}

	log.Printf("Loaded config from %s", path)
	log.Printf("  Server: %s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("  Printers: %d configured", len(cfg.Printers))

	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Server.Host == "" {
		cfg.Server.Host = "0.0.0.0"
	}
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 5001
	}
	if cfg.Printing.IPPUsername == "" {
		cfg.Printing.IPPUsername = "goprint"
	}
	if cfg.Printing.ManualDuplexHookTTL == "" {
		cfg.Printing.ManualDuplexHookTTL = "30m"
	}
	if cfg.Auth.Feishu.UserInfoURL == "" {
		cfg.Auth.Feishu.UserInfoURL = "https://open.feishu.cn/open-apis/authen/v1/user_info"
	}
	if cfg.Auth.Feishu.AuthorizeURL == "" {
		cfg.Auth.Feishu.AuthorizeURL = "https://accounts.feishu.cn/open-apis/authen/v1/authorize"
	}
	if cfg.Auth.Feishu.TokenURL == "" {
		cfg.Auth.Feishu.TokenURL = "https://open.feishu.cn/open-apis/authen/v2/oauth/token"
	}
	if cfg.Auth.Feishu.RequestTimeout == "" {
		cfg.Auth.Feishu.RequestTimeout = "3s"
	}
	if cfg.Auth.Feishu.TokenCacheTTL == "" {
		cfg.Auth.Feishu.TokenCacheTTL = "2m"
	}

	for i := range cfg.Printers {
		p := &cfg.Printers[i]
		if p.DuplexMode == "" {
			p.DuplexMode = "off"
		}
		if p.FirstPass == "" {
			p.FirstPass = "even"
		}
		if p.PadToEven == nil {
			v := true
			p.PadToEven = &v
		}
	}
}

func validateConfig(cfg *Config) error {
	if cfg.Auth.Enabled {
		if strings.TrimSpace(cfg.Auth.Feishu.AppID) == "" {
			return fmt.Errorf("auth.feishu.app_id is required when auth.enabled=true")
		}
		if strings.TrimSpace(cfg.Auth.Feishu.AppSecret) == "" {
			return fmt.Errorf("auth.feishu.app_secret is required when auth.enabled=true")
		}
		if _, err := parsePositiveDuration(cfg.Auth.Feishu.RequestTimeout, "auth.feishu.request_timeout"); err != nil {
			return err
		}
		if _, err := parsePositiveDuration(cfg.Auth.Feishu.TokenCacheTTL, "auth.feishu.token_cache_ttl"); err != nil {
			return err
		}
	}

	if len(cfg.Printers) == 0 {
		return fmt.Errorf("no printers configured")
	}

	seen := map[string]bool{}
	for _, p := range cfg.Printers {
		if p.ID == "" {
			return fmt.Errorf("printer id is required")
		}
		if p.URI == "" {
			return fmt.Errorf("printer uri is required for printer %s", p.ID)
		}
		if seen[p.ID] {
			return fmt.Errorf("duplicate printer id: %s", p.ID)
		}

		switch normalizeDuplexMode(p.DuplexMode) {
		case "off", "auto", "manual":
		default:
			return fmt.Errorf("invalid duplex_mode for printer %s: %s", p.ID, p.DuplexMode)
		}

		firstPass := strings.ToLower(strings.TrimSpace(p.FirstPass))
		if firstPass != "even" && firstPass != "odd" {
			return fmt.Errorf("invalid first_pass for printer %s: %s", p.ID, p.FirstPass)
		}

		seen[p.ID] = true
	}

	return nil
}

func parsePositiveDuration(raw string, field string) (time.Duration, error) {
	v, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || v <= 0 {
		return 0, fmt.Errorf("invalid %s: %s", field, raw)
	}
	return v, nil
}

func normalizeDuplexMode(mode string) string {
	value := strings.ToLower(strings.TrimSpace(mode))
	if value == "manuel" {
		return "manual"
	}
	return value
}

func (p PrinterConfig) NormalizedDuplexMode() string {
	return normalizeDuplexMode(p.DuplexMode)
}

func (p PrinterConfig) NormalizedFirstPass() string {
	v := strings.ToLower(strings.TrimSpace(p.FirstPass))
	if v != "odd" {
		return "even"
	}
	return "odd"
}

func (p PrinterConfig) PadToEvenEnabled() bool {
	if p.PadToEven == nil {
		return true
	}
	return *p.PadToEven
}

func (c *Config) GetPrinterByID(id string) (PrinterConfig, bool) {
	for _, p := range c.Printers {
		if p.ID == id {
			return p, true
		}
	}
	return PrinterConfig{}, false
}

func (c *Config) VisiblePrinters() []PrinterConfig {
	out := make([]PrinterConfig, 0)
	for _, p := range c.Printers {
		if p.Visible {
			out = append(out, p)
		}
	}
	return out
}
