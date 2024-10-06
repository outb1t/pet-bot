package main

import (
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"log"
	"math/rand"
	"os"
	"pet.outbid.goapp/api"
	"pet.outbid.goapp/db"
	"strconv"
	"strings"
)

var apiKey string

var bot *tgbotapi.BotAPI
var botUsername string

var systemPrompt string

var allowedChatID int64

func main() {
	botToken := getStringFromEnv("TELEGRAM_BOT_TOKEN")
	apiKey = getStringFromEnv("OPENAI_API_KEY")

	allowedChatID = getInt64FromEnv("ALLOWED_CHAT_ID")
	adminChatID := getInt64FromEnv("ADMIN_CHAT_ID")

	systemPromptData, err := os.ReadFile("system-prompt.md")
	if err != nil {
		log.Fatal(err)
	}
	systemPrompt = string(systemPromptData)
	fmt.Printf("systemPrompt: %s", systemPrompt)

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
	defer db.DB.Close()

	log.Printf("Authorized on account %s", botUsername)
	log.Printf("Bot restricted to chat ID: %d", allowedChatID)

	handleUpdates(adminChatID)
}

func handleUpdates(adminChatID int64) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	const workerCount = 5
	jobs := make(chan tgbotapi.Update, 100)

	for i := 0; i < workerCount; i++ {
		go worker(bot, jobs, adminChatID)
	}

	for update := range updates {
		if update.Message != nil {
			jobs <- update
		}
	}
}

func worker(bot *tgbotapi.BotAPI, jobs <-chan tgbotapi.Update, adminChatID int64) {
	for update := range jobs {
		handleUpdate(bot, update, adminChatID)
	}
}

func handleUpdate(bot *tgbotapi.BotAPI, update tgbotapi.Update, adminChatID int64) {
	if update.Message.Chat.ID != allowedChatID {
		alertMsg := tgbotapi.NewMessage(adminChatID, fmt.Sprintf("Unauthorized access attempt from chat ID: %d", update.Message.Chat.ID))
		sendMessage(alertMsg)
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
		handleCommand(update.Message)
	} else {
		handleMessage(update.Message)
	}
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

func handleMessage(message *tgbotapi.Message) {
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

	if strings.Contains(message.Text, botUsername) {
		handleMention(message)
	}
}

func sendMessage(msg tgbotapi.Chattable) {
	newMessage, err := bot.Send(msg)
	if err != nil {
		log.Printf("Error sending message: %v", err)
		return
	}
	savingError := db.SaveMessage(
		newMessage.MessageID,
		newMessage.Chat.ID,
		newMessage.From.ID,
		newMessage.Text,
		newMessage.Date,
	)

	if savingError != nil {
		log.Printf("Error saving message: %v", savingError)
	}
}

func handleCommand(message *tgbotapi.Message) {
	switch message.Command() {
	case "help":
		handleHelpCommand(message)
	case "getinfo":
		handleGetInfoCommand(message)
	case "gpt":
		handleGptCommand(message)
	default:
		handleUnknownCommand(message)
	}
}

func handleHelpCommand(message *tgbotapi.Message) {
	helpText := "Available commands:\n" +
		"/help - List available commands\n" +
		"/getinfo - Get your account information\n" +
		"/gpt - Forward message to gpt\n" +
		"Tag me @buddy_bro_pet_bot if you want to chat with me"
	msg := tgbotapi.NewMessage(message.Chat.ID, helpText)
	sendMessage(msg)
}

func handleGetInfoCommand(message *tgbotapi.Message) {
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
	sendMessage(msg)
}

func handleGptCommand(message *tgbotapi.Message) {
	args := message.CommandArguments()
	if args == "" {
		msg := tgbotapi.NewMessage(message.Chat.ID, "Please provide a message for GPT.")
		sendMessage(msg)
		return
	}

	requestBody := api.ChatCompletionRequest{
		Model: "gpt-4o",
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

	completionResponse, err := api.GetChatCompletion(apiKey, requestBody)
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
	msg.ReplyToMessageID = message.MessageID
	sendMessage(msg)
}

func handleMention(message *tgbotapi.Message) {
	text := message.Text
	if strings.Trim(text, ":?! ") == botUsername {
		phraseVar := fmt.Sprintf("BOT_DEFAULT_PHRASE%d", rand.Intn(8)+1)
		msg := tgbotapi.NewMessage(message.Chat.ID, getStringFromEnv(phraseVar))
		sendMessage(msg)
		return
	}

	messagesString, err := getFormattedMessages(30)
	if err != nil {
		log.Printf("Error getting formatted messages: %v", err)
		return
	}

	fullSystemPrompt := systemPrompt
	if messagesString != "" {
		fullSystemPrompt += "\nChat history:\n" + messagesString
	}

	requestBody := api.ChatCompletionRequest{
		Model: "gpt-4o",
		Messages: []api.Message{
			{
				Role:    "system",
				Content: fullSystemPrompt,
			},
			{
				Role:    "user",
				Content: text,
			},
		},
	}

	completionResponse, err := api.GetChatCompletion(apiKey, requestBody)
	if err != nil {
		fmt.Printf("Error getting chat completion: %v\n", err)
		return
	}

	var gptResponseText string
	if len(completionResponse.Choices) > 0 {
		gptResponseText = completionResponse.Choices[0].Message.Content
	} else {
		gptResponseText = "No choices in response"
	}

	msg := tgbotapi.NewMessage(message.Chat.ID, gptResponseText)
	msg.ReplyToMessageID = message.MessageID
	sendMessage(msg)
}

var usernames = make(map[int64]string)

func getFormattedMessages(limit int) (string, error) {
	messages, err := db.GetLastMessages(allowedChatID, limit)
	if err != nil {
		return "", fmt.Errorf("Error retrieving messages: %v", err)
	}

	var sb strings.Builder

	for _, msg := range messages {
		username, exists := usernames[msg.UserID]
		if !exists {
			chatMemberConfig := tgbotapi.GetChatMemberConfig{
				ChatConfigWithUser: tgbotapi.ChatConfigWithUser{
					ChatID: allowedChatID,
					UserID: msg.UserID,
				},
			}

			chatMember, err := bot.GetChatMember(chatMemberConfig)
			if err != nil {
				log.Printf("Error getting chat member for user ID %d: %v", msg.UserID, err)
				username = fmt.Sprintf("User%d", msg.UserID) // Fallback to user ID
			} else {
				if chatMember.User.UserName != "" {
					username = "@" + chatMember.User.UserName
				} else if chatMember.User.FirstName != "" || chatMember.User.LastName != "" {
					username = strings.TrimSpace(chatMember.User.FirstName + " " + chatMember.User.LastName)
				} else {
					username = fmt.Sprintf("User%d", msg.UserID)
				}
			}
			usernames[msg.UserID] = username
		}

		formattedDate := msg.Date.Format("02.01.2006 15:04:05")

		messageLine := fmt.Sprintf("%s %s : %s\n", formattedDate, username, msg.Text)
		sb.WriteString(messageLine)
	}

	return sb.String(), nil
}

func handleUnknownCommand(message *tgbotapi.Message) {
	msg := tgbotapi.NewMessage(message.Chat.ID, "Sorry, I don't recognize that command. Type /help to see available commands.")
	sendMessage(msg)
}
