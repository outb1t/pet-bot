package main

import (
	"fmt"
	"log"
	"os"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"pet.outbid.goapp/db"
)

func main() {
	initApp()

	go startWebServer()

	handleUpdates()
}

func initApp() {
	botToken := getStringFromEnv("TELEGRAM_BOT_TOKEN")
	allowedChatID = getInt64FromEnv("ALLOWED_CHAT_ID")
	testChatID = getInt64FromEnv("TEST_CHAT_ID")
	adminChatID = getInt64FromEnv("ADMIN_CHAT_ID")

	openAIToken = getStringFromEnv("OPENAI_API_KEY")
	gptModelForChatting = getStringFromEnv("GPT_MODEL_FOR_CHATTING")
	fmt.Printf("Bot chat model: %s\n", gptModelForChatting)
	gptModelForGptCommand = getStringFromEnv("GPT_MODEL_FOR_GPT_COMMAND")
	gptModelForWebSearch = getStringFromEnv("GPT_MODEL_FOR_WEB_SEARCH")
	fmt.Printf("Bot /gpt model: %s\n", gptModelForGptCommand)
	gptModelForRouting = os.Getenv("GPT_MODEL_FOR_ROUTING")
	if gptModelForRouting != "" {
		fmt.Printf("Bot routing model: %s\n", gptModelForRouting)
	}

	var botErr error
	bot, botErr = tgbotapi.NewBotAPI(botToken)
	if botErr != nil {
		log.Panic(botErr)
	}
	bot.Debug = os.Getenv("BOT_DEBUG") == "1"
	botUsername = "@" + bot.Self.UserName
	fmt.Printf("Bot name: %s\n", botUsername)

	if err := db.InitDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	log.Printf("Authorized on account %s", botUsername)
	log.Printf("Bot restricted to chat ID: %d", allowedChatID)
	log.Printf("Bot restricted to TEST chat ID: %d", testChatID)
}

func getStringFromEnv(name string) string {
	envVar := os.Getenv(name)
	if envVar == "" {
		log.Fatalf("%s environment variable not set", name)
	}
	return envVar
}

func getInt64FromEnv(name string) int64 {
	envVar := getStringFromEnv(name)
	envVarInt64, err := strconv.ParseInt(envVar, 10, 64)
	if err != nil {
		log.Fatalf("Invalid %s: %v", name, err)
	}
	return envVarInt64
}
