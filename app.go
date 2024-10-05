package main

import (
	"fmt"
	"log"
	"os"
	"pet.outbid.goapp/api"
	"pet.outbid.goapp/db"
	"strconv"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func main() {
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	if botToken == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN environment variable not set")
	}

	allowedChatID := getInt64FromEnv("ALLOWED_CHAT_ID")
	adminChatID := getInt64FromEnv("ADMIN_CHAT_ID")

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Panic(err)
	}

	if err := db.InitDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.DB.Close()

	bot.Debug = os.Getenv("BOT_DEBUG") == "1"

	log.Printf("Authorized on account %s", bot.Self.UserName)
	log.Printf("Bot restricted to chat ID: %d", allowedChatID)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	handleUpdates(bot, updates, allowedChatID, adminChatID)
}

func handleUpdates(bot *tgbotapi.BotAPI, updates tgbotapi.UpdatesChannel, allowedChatID int64, adminChatID int64) {
	const workerCount = 5
	jobs := make(chan tgbotapi.Update, 100)

	for i := 0; i < workerCount; i++ {
		go worker(bot, jobs, allowedChatID, adminChatID)
	}

	for update := range updates {
		if update.Message != nil {
			jobs <- update
		}
	}
}

func worker(bot *tgbotapi.BotAPI, jobs <-chan tgbotapi.Update, allowedChatID int64, adminChatID int64) {
	for update := range jobs {
		handleUpdate(bot, update, allowedChatID, adminChatID)
	}
}

func handleUpdate(bot *tgbotapi.BotAPI, update tgbotapi.Update, allowedChatID int64, adminChatID int64) {
	if update.Message.Chat.ID != allowedChatID {
		alertMsg := tgbotapi.NewMessage(adminChatID, fmt.Sprintf("Unauthorized access attempt from chat ID: %d", update.Message.Chat.ID))
		sendMessage(bot, alertMsg)
		if update.Message.Chat.IsPrivate() {
			fmt.Println("Private chat message, continue")
			return
		}
		log.Printf("Message from not allowed chat: %d, text: %s", update.Message.Chat.ID, update.Message.Text)
		leaveChat := tgbotapi.LeaveChatConfig{
			ChatID: update.Message.Chat.ID,
		}
		if _, err := bot.Request(leaveChat); err != nil {
			log.Printf("Failed to leave unauthorized chat: %v", err)
		} else {
			log.Printf("Left unauthorized chat ID: %d", update.Message.Chat.ID)
		}
		return
	}

	if update.Message.IsCommand() {
		handleCommand(bot, update.Message)
	} else {
		handleMessage(bot, update.Message)
	}
}

func getInt64FromEnv(name string) int64 {
	envVar := os.Getenv(name)
	if envVar == "" {
		log.Fatalf("%s environment variable not set", name)
	}
	envVarInt64, err := strconv.ParseInt(envVar, 10, 64)
	if err != nil {
		log.Fatalf("Invalid %s: %v", name, err)
	}
	return envVarInt64
}

func handleMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	var text string

	if message.Text != "" {
		text = message.Text
	} else if message.Caption != "" {
		text = message.Caption
	} else {
		log.Printf("Received non-text message without caption (message_id: %d), ignoring.", message.MessageID)
		return
	}

	err := db.SaveMessage(
		message.MessageID,
		message.Chat.ID,
		message.From.ID,
		text,
		message.Date,
	)
	if err != nil {
		log.Printf("Error saving message: %v", err)
	}
}

func sendMessage(bot *tgbotapi.BotAPI, msg tgbotapi.Chattable) {
	if _, err := bot.Send(msg); err != nil {
		log.Printf("Error sending message: %v", err)
	}
}

func handleCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	switch message.Command() {
	case "help":
		handleHelpCommand(bot, message)
	case "gettime":
		handleGetTimeCommand(bot, message)
	case "getinfo":
		handleGetInfoCommand(bot, message)
	case "gpt":
		handleGptCommand(bot, message)
	default:
		handleUnknownCommand(bot, message)
	}
}

func handleHelpCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	helpText := "Available commands:\n" +
		"/help - List available commands\n" +
		"/gettime - Get the current server time\n" +
		"/getinfo - Get your account information\n" +
		"/gpt - Forward message to gpt"
	msg := tgbotapi.NewMessage(message.Chat.ID, helpText)
	sendMessage(bot, msg)
}

func handleGetTimeCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	currentTime := time.Now().Format("Mon Jan 2 15:04:05 MST 2006")
	msg := tgbotapi.NewMessage(message.Chat.ID, "Current server time: "+currentTime)
	sendMessage(bot, msg)
}

func handleGetInfoCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	user := message.From
	info := "Your Account Information:\n" +
		"First Name: " + user.FirstName + "\n"

	if user.LastName != "" {
		info += "Last Name: " + user.LastName + "\n"
	}
	if user.UserName != "" {
		info += "Username: @" + user.UserName + "\n"
	}
	info += "User ID: " + strconv.FormatInt(int64(user.ID), 10)

	msg := tgbotapi.NewMessage(message.Chat.ID, info)
	sendMessage(bot, msg)
}

func handleGptCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	args := message.CommandArguments()
	if args == "" {
		msg := tgbotapi.NewMessage(message.Chat.ID, "Please provide a message for GPT.")
		sendMessage(bot, msg)
		return
	}

	requestBody := api.ChatCompletionRequest{
		Model: "gpt-3.5-turbo-0125",
		Messages: []api.Message{
			{
				Role:    "system",
				Content: "You are a helpful assistant.",
			},
			{
				Role:    "user",
				Content: args,
			},
		},
	}

	completionResponse, err := api.GetChatCompletion(os.Getenv("OPENAI_API_KEY"), requestBody)
	if err != nil {
		fmt.Printf("Error getting chat completion: %v\n", err)
		return
	}

	var txt string
	if len(completionResponse.Choices) > 0 {
		txt = completionResponse.Choices[0].Message.Content
	} else {
		txt = "No choices in response"
	}
	msg := tgbotapi.NewMessage(message.Chat.ID, txt)
	sendMessage(bot, msg)
}

func handleUnknownCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	msg := tgbotapi.NewMessage(message.Chat.ID, "Sorry, I don't recognize that command. Type /help to see available commands.")
	sendMessage(bot, msg)
}
