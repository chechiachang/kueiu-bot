package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

type Config struct {
	Telegram    TelegramConfig
	AzureOpenAI AzureOpenAIConfig
	Notion      NotionConfig
}

type TelegramConfig struct {
	BotToken string
}

type AzureOpenAIConfig struct {
	Endpoint   string
	APIKey     string
	Deployment string
}

type NotionConfig struct {
	Token      string
	DatabaseID string
}

func Load(path string, flags *pflag.FlagSet) (Config, error) {
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, errors.New("load .env")
	}

	v := viper.New()
	v.SetConfigFile(path)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	v.SetDefault("azure_openai.endpoint", "https://YOUR-RESOURCE.openai.azure.com/openai/v1/")
	v.SetDefault("azure_openai.deployment", "")
	v.SetDefault("notion.database_id", "")
	flagKeys := map[string]string{
		"azure_openai.endpoint":   "azure-openai-endpoint",
		"azure_openai.deployment": "azure-openai-deployment",
		"notion.database_id":      "notion-database-id",
	}
	for key, flagName := range flagKeys {
		if flag := flags.Lookup(flagName); flag != nil {
			if err := v.BindPFlag(key, flag); err != nil {
				return Config{}, fmt.Errorf("bind config flag %s: %w", flagName, err)
			}
		}
	}
	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) && !os.IsNotExist(err) {
			return Config{}, fmt.Errorf("read config file: %w", err)
		}
	}

	var cfg Config
	cfg.Telegram.BotToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	cfg.AzureOpenAI.Endpoint = strings.TrimRight(v.GetString("azure_openai.endpoint"), "/") + "/"
	cfg.AzureOpenAI.APIKey = os.Getenv("AZURE_OPENAI_API_KEY")
	cfg.AzureOpenAI.Deployment = v.GetString("azure_openai.deployment")
	cfg.Notion.Token = os.Getenv("NOTION_TOKEN")
	cfg.Notion.DatabaseID = v.GetString("notion.database_id")
	return cfg, nil
}

func (c Config) Validate() error {
	var missing []string
	if strings.TrimSpace(c.Telegram.BotToken) == "" {
		missing = append(missing, "TELEGRAM_BOT_TOKEN")
	}
	if strings.TrimSpace(c.AzureOpenAI.APIKey) == "" {
		missing = append(missing, "AZURE_OPENAI_API_KEY")
	}
	endpoint, err := url.ParseRequestURI(c.AzureOpenAI.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || strings.Contains(strings.ToLower(endpoint.Host), "your-resource") || !strings.Contains(endpoint.Path, "/openai/v1") {
		missing = append(missing, "AZURE_OPENAI_ENDPOINT")
	}
	if strings.TrimSpace(c.AzureOpenAI.Deployment) == "" {
		missing = append(missing, "AZURE_OPENAI_DEPLOYMENT")
	}
	if strings.TrimSpace(c.Notion.Token) == "" {
		missing = append(missing, "NOTION_TOKEN")
	}
	if strings.TrimSpace(c.Notion.DatabaseID) == "" {
		missing = append(missing, "NOTION_DATABASE_ID")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required configuration: %s", strings.Join(missing, ", "))
	}
	return nil
}
