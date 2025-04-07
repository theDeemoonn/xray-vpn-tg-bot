package bot

import (
	"context"
	"log/slog"

	gobot "github.com/go-telegram/bot"
	models "github.com/go-telegram/bot/models"
	// tu "github.com/mymmrac/telego/telegoutil" // Not needed for BotCommand struct
)

// setCommands registers the bot commands for the menu.
func (b *Bot) setCommands() error {
	commands := []models.BotCommand{
		{Command: "start", Description: "🚀 Запустить/Перезапустить бота"},
		{Command: "subscriptions", Description: "📄 Мои подписки"},
		{Command: "buy", Description: "🛒 Купить подписку"},
		{Command: "referral", Description: "🎁 Реферальная программа"},
		{Command: "faq", Description: "❓ Помощь и FAQ"},
	}

	// Use a background context for setting commands
	ctx := context.Background()

	_, err := b.api.SetMyCommands(ctx, &gobot.SetMyCommandsParams{
		Commands: commands,
	})
	if err != nil {
		b.logger.Error("Failed to set bot commands", slog.String("error", err.Error()))
		return err
	}
	b.logger.Info("Bot commands set successfully")
	return nil
}
