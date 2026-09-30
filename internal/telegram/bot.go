package telegram

import (
	"context"
	"errors"
	"strings"
	"sync"
	"unicode/utf16"

	"github.com/chechia/kueiu-bot/internal/config"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"github.com/sirupsen/logrus"
)

type assistant interface {
	Answer(context.Context, string) (string, error)
}

type Bot struct {
	client           *bot.Bot
	assistant        assistant
	username         string
	botID            int64
	log              *logrus.Logger
	firstUpdate      sync.Once
	firstGroupUpdate sync.Once
	firstTopicUpdate sync.Once
}

func New(cfg config.TelegramConfig, assistant assistant, log *logrus.Logger) (*Bot, error) {
	service := &Bot{
		assistant: assistant,
		log:       log,
	}
	client, err := bot.New(cfg.BotToken,
		bot.WithDefaultHandler(service.handle),
		bot.WithAllowedUpdates(bot.AllowedUpdates{"message", "channel_post"}),
		bot.WithWorkers(4),
		bot.WithSkipGetMe(),
		bot.WithErrorsHandler(func(err error) {
			if err == nil {
				log.Warn("Telegram polling error")
				return
			}
			description := err.Error()
			if cfg.BotToken != "" {
				description = strings.ReplaceAll(description, cfg.BotToken, "[redacted]")
			}
			log.WithField("error", description).Warn("Telegram polling error")
		}),
	)
	if err != nil {
		return nil, errors.New("create Telegram client")
	}
	service.client = client
	return service, nil
}

func (b *Bot) Start(ctx context.Context) error {
	me, err := b.client.GetMe(ctx)
	if err != nil {
		b.log.Warn("Telegram initialization failed")
		return errors.New("Telegram initialization failed")
	}
	b.username = me.Username
	b.botID = me.ID
	b.log.WithFields(logrus.Fields{
		"bot_username":                me.Username,
		"can_join_groups":             me.CanJoinGroups,
		"can_read_all_group_messages": me.CanReadAllGroupMessages,
	}).Info("Telegram bot started")
	if !me.CanReadAllGroupMessages {
		b.log.Warn("Telegram privacy mode is enabled: group @mentions alone are not delivered to non-admin bots; disable privacy with BotFather /setprivacy and re-add the bot to receive group messages")
	}
	b.client.Start(ctx)
	return nil
}

func (b *Bot) handle(ctx context.Context, _ *bot.Bot, update *models.Update) {
	message := update.Message
	updateType := "message"
	if message == nil {
		message = update.ChannelPost
		updateType = "channel_post"
	}
	if message == nil {
		return
	}
	text, entities := message.Text, message.Entities
	if text == "" {
		text, entities = message.Caption, message.CaptionEntities
	}
	b.firstUpdate.Do(func() {
		b.log.WithFields(logrus.Fields{
			"chat_type":    message.Chat.Type,
			"update_type":  updateType,
			"entity_types": entityTypes(entities),
		}).Info("Telegram polling received first message update")
	})
	if message.Chat.Type == "group" || message.Chat.Type == "supergroup" {
		b.firstGroupUpdate.Do(func() {
			b.log.WithFields(logrus.Fields{
				"chat_type":        message.Chat.Type,
				"update_type":      updateType,
				"is_topic_message": message.IsTopicMessage,
				"thread_id":        message.MessageThreadID,
				"has_from":         message.From != nil,
				"sender_chat_set":  message.SenderChat != nil,
				"entity_types":     entityTypes(entities),
			}).Info("Telegram polling received first group message")
		})
	}
	if message.Chat.Type == "supergroup" {
		b.firstTopicUpdate.Do(func() {
			b.log.WithFields(logrus.Fields{
				"is_topic_message": message.IsTopicMessage,
				"thread_id":        message.MessageThreadID,
				"entity_types":     entityTypes(entities),
			}).Info("Telegram polling received first supergroup message")
		})
	}
	request, mentioned := removeBotMention(text, entities, b.username, b.botID)
	if message.From != nil && message.From.IsBot {
		b.log.WithField("chat_type", message.Chat.Type).Info("ignored bot-authored message")
		return
	}
	if !mentioned {
		request = text
	}
	b.log.WithFields(logrus.Fields{
		"chat_type":        message.Chat.Type,
		"update_type":      updateType,
		"is_topic_message": message.IsTopicMessage,
		"thread_id":        message.MessageThreadID,
		"mention_entity":   mentioned,
	}).Info("processing accepted message")

	quoted := ""
	if message.ReplyToMessage != nil {
		quoted = message.ReplyToMessage.Text
		if quoted == "" {
			quoted = message.ReplyToMessage.Caption
		}
	}
	if strings.TrimSpace(request) == "" && strings.TrimSpace(quoted) == "" {
		b.log.Info("ignored bare bot mention without request or reply context")
		return
	}
	prompt := request
	if quoted != "" {
		prompt = "Quoted context:\n" + quoted + "\n\nUser request:\n" + request
	}
	answer, err := b.assistant.Answer(ctx, prompt)
	if err != nil {
		b.log.Warn("assistant request failed")
		answer = "I couldn't complete that request because the assistant service failed. Please try again."
	}
	if _, err := b.client.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:          message.Chat.ID,
		MessageThreadID: message.MessageThreadID,
		Text:            answer,
	}); err != nil {
		description := err.Error()
		if token := b.client.Token(); token != "" {
			description = strings.ReplaceAll(description, token, "[redacted]")
		}
		b.log.WithFields(logrus.Fields{
			"chat_type": message.Chat.Type,
			"thread_id": message.MessageThreadID,
			"error":     description,
		}).Warn("Telegram reply failed")
		return
	}
	b.log.WithFields(logrus.Fields{
		"chat_type": message.Chat.Type,
		"thread_id": message.MessageThreadID,
	}).Info("Telegram reply sent")
}

func entityTypes(entities []models.MessageEntity) []string {
	types := make([]string, 0, len(entities))
	for _, entity := range entities {
		types = append(types, string(entity.Type))
	}
	return types
}

func removeBotMention(text string, entities []models.MessageEntity, username string, botID int64) (string, bool) {
	for _, entity := range entities {
		switch string(entity.Type) {
		case "mention":
			mention, start, end, ok := entityText(text, entity.Offset, entity.Length)
			if !ok || !strings.EqualFold(strings.TrimPrefix(mention, "@"), username) {
				continue
			}
			return strings.TrimSpace(text[:start] + text[end:]), true
		case "text_mention":
			if entity.User != nil && entity.User.ID == botID {
				_, start, end, ok := entityText(text, entity.Offset, entity.Length)
				if ok {
					return strings.TrimSpace(text[:start] + text[end:]), true
				}
			}
		}
	}
	return "", false
}

func entityText(text string, offset, length int) (string, int, int, bool) {
	if offset < 0 || length <= 0 {
		return "", 0, 0, false
	}
	units := utf16.Encode([]rune(text))
	if offset+length > len(units) {
		return "", 0, 0, false
	}
	start := utf16OffsetToByte(text, offset)
	end := utf16OffsetToByte(text, offset+length)
	if start < 0 || end < start || end > len(text) {
		return "", 0, 0, false
	}
	return text[start:end], start, end, true
}

func utf16OffsetToByte(text string, target int) int {
	if target == 0 {
		return 0
	}
	units := 0
	for byteOffset, r := range text {
		if units == target {
			return byteOffset
		}
		units += len(utf16.Encode([]rune{r}))
		if units == target {
			return byteOffset + len(string(r))
		}
		if units > target {
			return -1
		}
	}
	if units == target {
		return len(text)
	}
	return -1
}
