package main

import (
	"log"

	"fmt"

	"github.com/joho/godotenv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var GlobalConfig *Config

func init() {
	err := godotenv.Load()
	if err != nil {
		log.Panic(err)
		fmt.Println("Error loading .env file")
		return
	}
	GlobalConfig = NewConfig()
	initDB()
}

func main() {


	bot, err := tgbotapi.NewBotAPI(GlobalConfig.BotToken)
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = true

	log.Printf("Authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message != nil { // If we got a message
			log.Printf("[%s] %s", update.Message.From.UserName, update.Message.Text)

			msg := tgbotapi.NewMessage(update.Message.Chat.ID, update.Message.Text)
			msg.ReplyToMessageID = update.Message.MessageID

			bot.Send(msg)
		}
	}
}
