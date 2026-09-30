package cmd

import (
	"fmt"
	"os"

	"github.com/chechia/kueiu-bot/internal/agent"
	"github.com/chechia/kueiu-bot/internal/config"
	"github.com/chechia/kueiu-bot/internal/notion"
	"github.com/chechia/kueiu-bot/internal/telegram"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func Execute() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	var configPath string
	root := &cobra.Command{
		Use:           "kueiu-bot",
		Short:         "Private Telegram assistant for a Notion database",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&configPath, "config", "config.yaml", "configuration file")

	serve := &cobra.Command{
		Use:   "serve",
		Short: "Start the Telegram bot",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath, cmd.Flags())
			if err != nil {
				return err
			}
			if err := cfg.Validate(); err != nil {
				return err
			}

			log := logrus.New()
			log.SetFormatter(&logrus.JSONFormatter{})
			notionClient := notion.NewClient(cfg.Notion.Token, cfg.Notion.DatabaseID)
			assistant := agent.New(cfg.AzureOpenAI.Endpoint, cfg.AzureOpenAI.APIKey, cfg.AzureOpenAI.Deployment, notionClient, log)
			bot, err := telegram.New(cfg.Telegram, assistant, log)
			if err != nil {
				return fmt.Errorf("initialize Telegram bot: %w", err)
			}

			return bot.Start(cmd.Context())
		},
	}
	serve.Flags().String("azure-openai-endpoint", "", "Azure OpenAI Responses API endpoint")
	serve.Flags().String("azure-openai-deployment", "", "Azure OpenAI deployment name")
	serve.Flags().String("notion-database-id", "", "Notion database ID")
	root.AddCommand(serve)
	return root
}
