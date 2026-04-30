package config

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 应用配置
type Config struct {
	Server           ServerConfig           `yaml:"server"`
	Auth             AuthConfig             `yaml:"auth"`
	SaneAPI          SaneAPIConfig          `yaml:"sane_api"`
	JobStore         JobStoreConfig         `yaml:"job_store"`
	Printing         PrintingConfig         `yaml:"printing"`
	OfficeConversion OfficeConversionConfig `yaml:"office_conversion"`
	Printers         []PrinterConfig              `yaml:"printers"`
	Bot              BotConfig                    `yaml:"bot"`
	FileTypeDefaults map[string]FileTypeDefault   `yaml:"file_type_defaults"`
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

// SaneAPIConfig scanservjs 反向代理配置
type SaneAPIConfig struct {
	AuthEnabled *bool  `yaml:"auth_enabled"`
	TargetURL   string `yaml:"target_url"`
	AuthHeader  string `yaml:"auth_header"`
	AuthToken   string `yaml:"auth_token"`
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

// JobStoreConfig 打印任务存储配置
type JobStoreConfig struct {
	Enabled bool                `yaml:"enabled"`
	Feishu  FeishuBitableConfig `yaml:"feishu"`
}

// FeishuBitableConfig 飞书多维表配置
type FeishuBitableConfig struct {
	AppToken       string `yaml:"app_token"`
	TableID        string `yaml:"table_id"`
	RequestTimeout string `yaml:"request_timeout"`
}

// PrintingConfig 打印相关全局配置
type PrintingConfig struct {
	IPPUsername         string `yaml:"ipp_username"`
	ManualDuplexHookTTL string `yaml:"manual_duplex_hook_ttl"`
}

// OfficeConversionConfig Office 文档转换配置（通过 Python gRPC 服务）
type OfficeConversionConfig struct {
	Enabled         bool     `yaml:"enabled"`
	StartWithServer bool     `yaml:"start_with_server"`
	ServiceScript   string   `yaml:"service_script"`
	AcceptedFormats []string `yaml:"accepted_formats"`
	GRPCAddress     string   `yaml:"grpc_address"`
	RequestTimeout  string   `yaml:"request_timeout"`
	OutputDir       string   `yaml:"output_dir"`
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

// BotConfig 飞书 Bot 配置
type BotConfig struct {
	Enabled           bool   `yaml:"enabled"`
	VerificationToken string `yaml:"verification_token"`
	EncryptKey        string `yaml:"encrypt_key"`
	BotName           string `yaml:"bot_name"`
	CardTimeout       string `yaml:"card_timeout"`
	WorkDir           string `yaml:"work_dir"`
}

// FileTypeDefault 按文件扩展名的默认打印参数
type FileTypeDefault struct {
	Ref       string `yaml:"$ref"`
	Copies    int    `yaml:"copies"`
	Duplex    string `yaml:"duplex"`
	Nup       int    `yaml:"nup"`
	Collate   *bool  `yaml:"collate"`
	Direction string `yaml:"direction"`
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
	if cfg.OfficeConversion.GRPCAddress == "" {
		cfg.OfficeConversion.GRPCAddress = "127.0.0.1:50061"
	}
	if cfg.OfficeConversion.ServiceScript == "" {
		cfg.OfficeConversion.ServiceScript = "office_converter/run.sh"
	}
	if len(cfg.OfficeConversion.AcceptedFormats) == 0 {
		cfg.OfficeConversion.AcceptedFormats = []string{"doc", "docx", "ppt", "pptx"}
	}
	normalizedFormats := make([]string, 0, len(cfg.OfficeConversion.AcceptedFormats))
	for _, f := range cfg.OfficeConversion.AcceptedFormats {
		nf := normalizeOfficeFormat(f)
		if nf != "" {
			normalizedFormats = append(normalizedFormats, nf)
		}
	}
	if len(normalizedFormats) == 0 {
		normalizedFormats = []string{"doc", "docx", "ppt", "pptx"}
	}
	cfg.OfficeConversion.AcceptedFormats = normalizedFormats
	if cfg.OfficeConversion.RequestTimeout == "" {
		cfg.OfficeConversion.RequestTimeout = "60s"
	}
	if cfg.OfficeConversion.OutputDir == "" {
		cfg.OfficeConversion.OutputDir = "/tmp/office-output"
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
	if cfg.SaneAPI.TargetURL == "" {
		cfg.SaneAPI.TargetURL = "http://192.168.101.37:8080"
	}
	if cfg.SaneAPI.AuthEnabled == nil {
		enabled := true
		cfg.SaneAPI.AuthEnabled = &enabled
	}
	if cfg.SaneAPI.AuthHeader == "" {
		cfg.SaneAPI.AuthHeader = "X-Sane-Api-Key"
	}
	if cfg.JobStore.Feishu.RequestTimeout == "" {
		cfg.JobStore.Feishu.RequestTimeout = "3s"
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

	if cfg.Bot.BotName == "" {
		cfg.Bot.BotName = "GoPrint"
	}
	if cfg.Bot.CardTimeout == "" {
		cfg.Bot.CardTimeout = "10m"
	}
	if cfg.Bot.WorkDir == "" {
		cfg.Bot.WorkDir = "/tmp/bot-files"
	}

	cfg.FileTypeDefaults = resolveFileTypeRefs(cfg.FileTypeDefaults)
}

	if cfg.Bot.BotName == "" {
		cfg.Bot.BotName = "GoPrint"
	}
	if cfg.Bot.CardTimeout == "" {
		cfg.Bot.CardTimeout = "10m"
	}
	if cfg.Bot.WorkDir == "" {
		cfg.Bot.WorkDir = "/tmp/bot-files"
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

	if cfg.JobStore.Enabled {
		if strings.TrimSpace(cfg.JobStore.Feishu.AppToken) == "" {
			return fmt.Errorf("job_store.feishu.app_token is required when job_store.enabled=true")
		}
		if strings.TrimSpace(cfg.JobStore.Feishu.TableID) == "" {
			return fmt.Errorf("job_store.feishu.table_id is required when job_store.enabled=true")
		}
		if _, err := parsePositiveDuration(cfg.JobStore.Feishu.RequestTimeout, "job_store.feishu.request_timeout"); err != nil {
			return err
		}
	}

	if cfg.OfficeConversion.Enabled {
		if len(cfg.OfficeConversion.AcceptedFormats) == 0 {
			return fmt.Errorf("office_conversion.accepted_formats must not be empty when office_conversion.enabled=true")
		}
		for _, f := range cfg.OfficeConversion.AcceptedFormats {
			switch normalizeOfficeFormat(f) {
			case "doc", "docx", "ppt", "pptx":
			default:
				return fmt.Errorf("unsupported office_conversion.accepted_formats entry: %s", f)
			}
		}
		if strings.TrimSpace(cfg.OfficeConversion.GRPCAddress) == "" {
			return fmt.Errorf("office_conversion.grpc_address is required when office_conversion.enabled=true")
		}
		if _, err := parsePositiveDuration(cfg.OfficeConversion.RequestTimeout, "office_conversion.request_timeout"); err != nil {
			return err
		}
		if strings.TrimSpace(cfg.OfficeConversion.OutputDir) == "" {
			return fmt.Errorf("office_conversion.output_dir is required when office_conversion.enabled=true")
		}
		if cfg.OfficeConversion.StartWithServer {
			if strings.TrimSpace(cfg.OfficeConversion.ServiceScript) == "" {
				return fmt.Errorf("office_conversion.service_script is required when office_conversion.start_with_server=true")
			}
			if _, err := os.Stat(filepath.Clean(cfg.OfficeConversion.ServiceScript)); err != nil {
				return fmt.Errorf("office_conversion.service_script is not accessible: %w", err)
			}
		}
	}

	if strings.TrimSpace(cfg.SaneAPI.TargetURL) == "" {
		return fmt.Errorf("sane_api.target_url is required")
	}
	if _, err := url.ParseRequestURI(strings.TrimSpace(cfg.SaneAPI.TargetURL)); err != nil {
		return fmt.Errorf("invalid sane_api.target_url: %w", err)
	}
	if cfg.SaneAPI.IsAuthEnabled() && strings.TrimSpace(cfg.SaneAPI.AuthHeader) == "" {
		return fmt.Errorf("sane_api.auth_header is required")
	}
	if cfg.SaneAPI.IsAuthEnabled() && strings.TrimSpace(cfg.SaneAPI.AuthToken) == "" && !cfg.Auth.Enabled {
		return fmt.Errorf("sane_api.auth_enabled=true requires sane_api.auth_token or auth.enabled=true")
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

func normalizeOfficeFormat(raw string) string {
	v := strings.TrimSpace(strings.ToLower(raw))
	v = strings.TrimPrefix(v, ".")
	return v
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

func (s SaneAPIConfig) IsAuthEnabled() bool {
	if s.AuthEnabled == nil {
		return true
	}
	return *s.AuthEnabled
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

// resolveFileTypeRefs 展开 $ref 引用。使用拓扑排序解析依赖，循环引用会被跳过。
func resolveFileTypeRefs(raw map[string]FileTypeDefault) map[string]FileTypeDefault {
	if raw == nil {
		raw = make(map[string]FileTypeDefault)
	}

	cut := make(map[string]FileTypeDefault, len(raw))
	for k, v := range raw {
		cut[k] = v
	}

	inDegree := make(map[string]int)
	adj := make(map[string][]string)
	for ext := range cut {
		inDegree[ext] = 0
	}

	for ext, def := range cut {
		ref := strings.TrimSpace(def.Ref)
		if ref == "" {
			continue
		}
		if _, ok := cut[ref]; !ok {
			log.Printf("[config] file_type_defaults.%s $ref=%s not found, ignoring", ext, ref)
			continue
		}
		if ref == ext {
			log.Printf("[config] file_type_defaults.%s self-referencing $ref, ignoring", ext)
			continue
		}
		adj[ref] = append(adj[ref], ext)
		inDegree[ext]++
	}

	var queue []string
	for ext := range cut {
		if inDegree[ext] == 0 {
			queue = append(queue, ext)
		}
	}
	sort.Strings(queue)

	result := make(map[string]FileTypeDefault, len(cut))
	for len(queue) > 0 {
		ext := queue[0]
		queue = queue[1:]
		def := cut[ext]
		ref := strings.TrimSpace(def.Ref)
		if ref != "" {
			if resolved, ok := result[ref]; ok {
				def = resolved
			}
		}
		result[ext] = def
		for _, next := range adj[ext] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}

	return result
}

// ResolveFileTypeDefault 根据文件名查询默认打印参数
func (c *Config) ResolveFileTypeDefault(filename string) FileTypeDefault {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
	if ext == "" {
		return hardcodedDefault()
	}
	if def, ok := c.FileTypeDefaults[ext]; ok {
		return def
	}
	return hardcodedDefault()
}

func hardcodedDefault() FileTypeDefault {
	v := true
	return FileTypeDefault{
		Copies:    1,
		Duplex:    "off",
		Nup:       1,
		Collate:   &v,
		Direction: "horizontal",
	}
}
