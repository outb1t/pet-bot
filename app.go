package main

import (
	"encoding/base64"
	"fmt"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"html/template"
	"log"
	"math/rand"
	"net/http"
	"os"
	"pet.outbid.goapp/api"
	"pet.outbid.goapp/db"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var bot *tgbotapi.BotAPI
var botUsername string
var allowedChatID int64
var testChatID int64
var adminChatID int64

var openAIToken string
var gptModelForChatting string
var gptModelForGptCommand string

var (
	usernames     = make(map[int64]string)
	usernamesLock sync.RWMutex
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
	fmt.Printf("Bot /gpt model: %s\n", gptModelForGptCommand)

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

func startWebServer() {
	username := getStringFromEnv("BASIC_AUTH_USERNAME")
	password := getStringFromEnv("BASIC_AUTH_PASSWORD")

	fs := http.FileServer(http.Dir("web/static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("/", basicAuth(username, password, indexHandler))
	http.HandleFunc("/save", basicAuth(username, password, saveHandler))
	port := os.Getenv("WEB_SERVER_PORT")
	if port == "" {
		port = "8080"
	}
	log.Println("Starting web server on : " + port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Printf("Failed to start web server: %v", err)
	}
}

func basicAuth(username, password string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "" {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		const prefix = "Basic "
		if !strings.HasPrefix(auth, prefix) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		decoded, err := base64.StdEncoding.DecodeString(auth[len(prefix):])
		if err != nil {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		credentials := strings.SplitN(string(decoded), ":", 2)
		if len(credentials) != 2 {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}

		reqUsername, reqPassword := credentials[0], credentials[1]
		if reqUsername != username || reqPassword != password {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}

		handler.ServeHTTP(w, r)
	}
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	templateContent, err := os.ReadFile("web/index.html")
	if err != nil {
		fmt.Println(err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	var formTemplate = template.Must(template.New("form").Parse(string(templateContent)))

	promptText, err := db.GetSystemPrompt(false)
	if err != nil {
		fmt.Println(err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	data := struct {
		Prompt string
	}{
		Prompt: promptText,
	}

	w.Header().Set("Content-Type", "text/html")
	if err := formTemplate.Execute(w, data); err != nil {
		http.Error(w, "Failed to render template", http.StatusInternalServerError)
	}
}

func saveHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}

	prompt := r.FormValue("prompt")
	if prompt == "" {
		http.Error(w, "Prompt cannot be empty", http.StatusBadRequest)
		return
	}

	err := db.InsertPrompt(prompt, 1)
	if err != nil {
		http.Error(w, "Failed to save prompt", http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleUpdates() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	const workerCount = 5
	jobs := make(chan tgbotapi.Update, 100)

	for i := 0; i < workerCount; i++ {
		go worker(bot, jobs)
	}

	for update := range updates {
		if update.Message != nil {
			jobs <- update
		}
	}
}

func worker(bot *tgbotapi.BotAPI, jobs <-chan tgbotapi.Update) {
	for update := range jobs {
		handleUpdate(bot, update)
	}
}

func handleUpdate(bot *tgbotapi.BotAPI, update tgbotapi.Update) {
	if update.Message.Chat.ID != allowedChatID && update.Message.Chat.ID != testChatID {
		alertMsg := tgbotapi.NewMessage(adminChatID, fmt.Sprintf("Unauthorized access attempt from chat ID: %d", update.Message.Chat.ID))
		sendMessage(alertMsg, false)
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

	replyToBotMessage := message.ReplyToMessage != nil && bot.Self.ID == message.ReplyToMessage.From.ID
	if isBotMentioned(text) || replyToBotMessage {
		handleMention(message)
	} else {
		saveMessage(message, text)
	}
}

func isBotMentioned(text string) bool {
	return strings.Contains(text, botUsername)
}

func sendMessage(msg tgbotapi.Chattable, saveOptions ...bool) {
	save := true
	if len(saveOptions) > 0 {
		save = saveOptions[0]
	}

	newMessage, err := bot.Send(msg)
	if err != nil {
		log.Printf("Error sending message: %v", err)
	}

	if save {
		saveMessage(&newMessage)
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
	sendMessage(msg, false)
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
	info += "User ID: " + strconv.FormatInt(user.ID, 10)

	msg := tgbotapi.NewMessage(message.Chat.ID, info)
	sendMessage(msg, false)
}

func handleGptCommand(message *tgbotapi.Message) {
	args := message.CommandArguments()
	if args == "" {
		msg := tgbotapi.NewMessage(message.Chat.ID, "Please provide a message for GPT.")
		sendMessage(msg, false)
		return
	}
	saveMessage(message, args)

	requestBody := api.ChatCompletionRequest{
		Model: gptModelForGptCommand,
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

	completionResponse, err := api.GetChatCompletion(openAIToken, requestBody)
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
		// here is the only case when we don't save incoming message, because there is only @%bot_nickname%,
		//but we save output bot phrase
		phraseVar := fmt.Sprintf("BOT_DEFAULT_PHRASE%d", rand.Intn(8)+1)
		msg := tgbotapi.NewMessage(message.Chat.ID, getStringFromEnv(phraseVar))
		sendMessage(msg)
		return
	}

	saveMessage(message)

	messagesString, err := getFormattedMessages(message.Chat.ID, 40)
	if err != nil {
		log.Printf("Error getting formatted messages: %v", err)
		return
	}

	systemPrompt, err := db.GetSystemPrompt(true)
	currentDate := strings.ToUpper(time.Now().Format("02-Mar-2006 15:04:05"))
	systemPrompt = strings.Replace(systemPrompt, "%current_date%", currentDate, 1)
	if err != nil {
		log.Fatal(err)
	}
	if messagesString != "" {
		systemPrompt += "\n\n**Chat history:**\n" + messagesString
	}

	if message.ReplyToMessage != nil {
		replyMessageId := message.ReplyToMessage.MessageID
		if bot.Self.ID == message.ReplyToMessage.From.ID {
			text = fmt.Sprintf("\nthis is reply to your msg%d:\n ", replyMessageId) + text
		} else if isBotMentioned(text) {
			text = fmt.Sprintf("You were mentioned to reply to the message msg%d by this message:", replyMessageId) + text
		}
	}

	requestBody := api.ChatCompletionRequest{
		Model: gptModelForChatting,
		Messages: []api.Message{
			{
				Role:    "system",
				Content: systemPrompt,
			},
			{
				Role:    "user",
				Content: text,
			},
		},
	}

	completionResponse, err := api.GetChatCompletion(openAIToken, requestBody)
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

func getFormattedMessages(chatId int64, limit int) (string, error) {
	messages, err := db.GetLastMessages(chatId, limit)
	if err != nil {
		return "", fmt.Errorf("Error retrieving messages: %v", err)
	}

	var sb strings.Builder

	for _, msg := range messages {
		usernamesLock.RLock()
		username, exists := usernames[msg.UserID]
		usernamesLock.RUnlock()

		if !exists {
			chatMemberConfig := tgbotapi.GetChatMemberConfig{
				ChatConfigWithUser: tgbotapi.ChatConfigWithUser{
					ChatID: chatId,
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

			usernamesLock.Lock()
			usernames[msg.UserID] = username
			usernamesLock.Unlock()
		}

		formattedDate := msg.Date.Format("02.01.2006 15:04:05")

		if msg.UserID == bot.Self.ID && utf8.RuneCountInString(msg.Text) > 400 {
			fmt.Printf("\nSkipping bot message %d", msg.MessageID)
			continue
		}
		messageLine := fmt.Sprintf("msg%d %s %s : %s\n", msg.MessageID, formattedDate, username, msg.Text)
		sb.WriteString(messageLine)
	}

	return sb.String(), nil
}

func handleUnknownCommand(message *tgbotapi.Message) {
	msg := tgbotapi.NewMessage(message.Chat.ID, "Sorry, I don't recognize that command. Type /help to see available commands.")
	sendMessage(msg, false)
}

func saveMessage(message *tgbotapi.Message, texts ...string) {
	var text string
	if len(texts) > 0 {
		text = texts[0]
	} else {
		text = message.Text
	}

	if text == "" {
		log.Printf("Skip saving, empty message from user: %s", message.From.UserName)
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
