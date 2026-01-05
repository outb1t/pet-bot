package main

import (
	"encoding/base64"
	"fmt"
	"html"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"pet.outbid.goapp/api"
	"pet.outbid.goapp/db"
)

var bot *tgbotapi.BotAPI
var botUsername string
var allowedChatID int64
var testChatID int64
var adminChatID int64

var openAIToken string
var gptModelForChatting string
var gptModelForGptCommand string
var gptModelForWebSearch string
var gptModelForRouting string

var (
	usernames     = make(map[int64]string)
	usernamesLock sync.RWMutex
)

// Minimal HTML formatting for Telegram: keeps code blocks, converts headers
// to bold, supports bold/italic and inline links, and escapes the rest.
func formatHTML(text string) string {
	lines := strings.Split(text, "\n")
	var b strings.Builder
	inCode := false

	hrRe := regexp.MustCompile(`^-{3,}$`)
	linkRe := regexp.MustCompile(`\[(.+?)\]\((https?://[^\s)]+)\)`)
	boldRe := regexp.MustCompile(`\*\*(.+?)\*\*`)
	italicRe := regexp.MustCompile(`\*(.+?)\*`)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				b.WriteString("</code></pre>")
				inCode = false
			} else {
				b.WriteString("<pre><code>")
				inCode = true
			}
			if i < len(lines)-1 {
				b.WriteString("\n")
			}
			continue
		}

		if inCode {
			b.WriteString(html.EscapeString(line))
		} else {
			if hrRe.MatchString(trimmed) {
				dashCount := len(trimmed)
				if dashCount < 3 {
					dashCount = 3
				}
				line = "<b>" + strings.Repeat("&mdash;", dashCount) + "</b>"
				b.WriteString(line)
				if i < len(lines)-1 {
					b.WriteString("\n")
				}
				continue
			}

			if strings.HasPrefix(trimmed, "#") {
				content := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
				line = "<b>" + html.EscapeString(content) + "</b>"
			} else {
				escaped := html.EscapeString(line)
				escaped = linkRe.ReplaceAllStringFunc(escaped, func(s string) string {
					m := linkRe.FindStringSubmatch(s)
					return `<a href="` + html.EscapeString(m[2]) + `">` + html.EscapeString(m[1]) + `</a>`
				})
				escaped = boldRe.ReplaceAllStringFunc(escaped, func(s string) string {
					m := boldRe.FindStringSubmatch(s)
					return "<b>" + html.EscapeString(m[1]) + "</b>"
				})
				escaped = italicRe.ReplaceAllStringFunc(escaped, func(s string) string {
					m := italicRe.FindStringSubmatch(s)
					return "<i>" + html.EscapeString(m[1]) + "</i>"
				})
				line = escaped
			}
			b.WriteString(line)
		}

		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}

	if inCode {
		b.WriteString("</code></pre>")
	}

	return b.String()
}

// Telegram MarkdownV2 does not support headings, so we convert them to bold
// and escape special characters, while preserving fenced code blocks.
func formatMarkdownV2(text string) string {
	lines := strings.Split(text, "\n")
	var b strings.Builder
	inCode := false

	hrRe := regexp.MustCompile(`^-{3,}$`)
	escape := strings.NewReplacer(
		`_`, `\_`,
		`~`, `\~`,
		"`", "\\`",
		`>`, `\>`,
		`#`, `\#`,
		`=`, `\=`,
		`|`, `\|`,
		`{`, `\{`,
		`}`, `\}`,
		`\`, `\\`,
	)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			if inCode {
				b.WriteString("```")
				if i < len(lines)-1 {
					b.WriteString("\n")
				}
				inCode = false
			} else {
				lang := strings.TrimPrefix(trimmed, "```")
				b.WriteString("```")
				b.WriteString(lang)
				if i < len(lines)-1 {
					b.WriteString("\n")
				}
				inCode = true
			}
			continue
		}

		if inCode {
			b.WriteString(line)
		} else {
			if hrRe.MatchString(trimmed) {
				line = strings.Repeat("-", len(trimmed))
				b.WriteString(line)
				if i < len(lines)-1 {
					b.WriteString("\n")
				}
				continue
			}

			// Replace markdown-style headers (e.g., "### Title") with bold text.
			if strings.HasPrefix(trimmed, "#") {
				parts := strings.Fields(trimmed)
				if len(parts) > 1 {
					line = "*" + strings.TrimSpace(strings.Join(parts[1:], " ")) + "*"
				}
			}
			line = escape.Replace(line)
			b.WriteString(line)
		}

		if i < len(lines)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

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
	} else if len(message.Photo) > 0 {
		// If the message contains a photo with no text/caption
		replyToBotMessage := message.ReplyToMessage != nil && bot.Self.ID == message.ReplyToMessage.From.ID
		if replyToBotMessage {
			// If it's a reply to the bot's message, handle it as a bot mention (including the image)
			handleMention(message)
		} else {
			log.Printf("Received image without text (message_id: %d), ignoring.", message.MessageID)
		}
		return
	} else if message.Sticker != nil {
		// Sticker without text/caption
		replyToBotMessage := message.ReplyToMessage != nil && bot.Self.ID == message.ReplyToMessage.From.ID
		if replyToBotMessage {
			// Reply to bot with a sticker -> treat as mention with image
			handleMention(message)
		} else {
			log.Printf("Received sticker without text (message_id: %d), ignoring.", message.MessageID)
		}
		return
	} else {
		// No text, no caption, no photo: ignore other non-text messages (stickers, etc.)
		log.Printf("Received non-text message without caption (message_id: %d), ignoring.", message.MessageID)
		return
	}

	// Determine if the message is addressing the bot
	replyToBotMessage := message.ReplyToMessage != nil && bot.Self.ID == message.ReplyToMessage.From.ID
	if isBotMentioned(text) || replyToBotMessage {
		handleMention(message)
	} else {
		saveMessage(message, text)
	}
}

func downloadImageAsDataURL(message *tgbotapi.Message) (string, error) {
	if len(message.Photo) == 0 && message.Sticker == nil {
		return "", fmt.Errorf("no photo or sticker in message")
	}

	var fileID string

	if len(message.Photo) > 0 {
		// Use highest resolution photo (last in slice)
		photo := message.Photo[len(message.Photo)-1]
		fileID = photo.FileID
	} else if message.Sticker != nil {
		// Skip animated stickers, they are not simple images
		if message.Sticker.IsAnimated {
			return "", fmt.Errorf("animated stickers are not supported")
		}
		fileID = message.Sticker.FileID
	}

	file, err := bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return "", fmt.Errorf("failed to get file info from Telegram: %w", err)
	}

	fileURL := file.Link(bot.Token)

	resp, err := http.Get(fileURL)
	if err != nil {
		return "", fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read image body: %w", err)
	}

	// Size limit, e.g. 2 MB
	const maxImageSize = 2 * 1024 * 1024
	if len(data) > maxImageSize {
		return "", fmt.Errorf("image too large (%d bytes), limit is %d bytes", len(data), maxImageSize)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" || !strings.HasPrefix(strings.ToLower(contentType), "image/") {
		contentType = "image/jpeg"
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	dataURL := fmt.Sprintf("data:%s;base64,%s", contentType, b64)

	return dataURL, nil
}

func isBotMentioned(text string) bool {
	return strings.Contains(text, botUsername)
}

func sendMessage(msg tgbotapi.MessageConfig, saveOptions ...bool) {
	originalText := msg.Text
	save := true
	if len(saveOptions) > 0 {
		save = saveOptions[0]
	}

	const longMsgThreshold = 300

	if msg.ParseMode == tgbotapi.ModeMarkdownV2 {
		msg.ParseMode = tgbotapi.ModeHTML
		formatted := formatHTML(originalText)
		if utf8.RuneCountInString(originalText) > longMsgThreshold {
			// Try Telegram's expandable blockquote entity via HTML.
			msg.Text = `<blockquote expandable="true">` + formatted + `</blockquote>`
		} else {
			msg.Text = formatted
		}
	}

	newMessage, err := bot.Send(msg)
	if err != nil {
		// If markdown parsing failed – retry without formatting
		if strings.Contains(err.Error(), "can't parse entities") {
			log.Printf("Markdown parse error: %v, retrying without parse_mode", err)
			msg.ParseMode = ""
			msg.Text = originalText
			newMessage, err = bot.Send(msg)
		}
	}

	if err != nil {
		log.Printf("Error sending message: %v", err)
		_, _ = bot.Send(tgbotapi.NewMessage(msg.ChatID,
			fmt.Sprintf("Error sending message: %v", err)))
		return
	}

	if save {
		saveMessage(&newMessage, originalText)
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
		"Tag me @buddy_bro_pet_bot if you want to chat with me\n" +
		"Если использовать \"загугли\", \"поищи\" или ссылку в сообщении, то будет веб поиск(очень долго думает секунд 30-60)"
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

	reasoning := "high"
	verbosity := "medium"
	requestBody := api.ChatCompletionRequest{
		Model:           gptModelForChatting,
		ReasoningEffort: &reasoning,
		Verbosity:       &verbosity,
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
		txt = messageContentToString(completionResponse.Choices[0].Message.Content)
	} else {
		txt = "No choices in response"
	}
	msg := tgbotapi.NewMessage(message.Chat.ID, txt)
	msg.ReplyToMessageID = message.MessageID
	msg.ParseMode = tgbotapi.ModeMarkdownV2
	sendMessage(msg)
}
func handleMention(message *tgbotapi.Message) {
	text := message.Text
	if text == "" && message.Caption != "" {
		text = message.Caption
	}
	//if strings.Trim(text, ":?! ") == botUsername {
	//	// here is the only case when we don't save an incoming message, because there is only @%bot_nickname%,
	//	// but we save output bot phrase
	//	phraseVar := fmt.Sprintf("BOT_DEFAULT_PHRASE%d", rand.Intn(8)+1)
	//	msg := tgbotapi.NewMessage(message.Chat.ID, getStringFromEnv(phraseVar))
	//	sendMessage(msg)
	//	return
	//}

	saveMessage(message)

	// If replying to a message, prepend context info to the user text as before
	if message.ReplyToMessage != nil {
		replyMessageId := message.ReplyToMessage.MessageID
		if bot.Self.ID == message.ReplyToMessage.From.ID {
			// Reply to a bot message
			if text == "" {
				// If no user text, still indicate reply context
				text = fmt.Sprintf("this is reply to your msg%d:", replyMessageId)
			} else {
				text = fmt.Sprintf("this is reply to your msg%d:\n ", replyMessageId) + text
			}
		} else if isBotMentioned(text) {
			// Bot was mentioned in a reply to someone else's message
			text = fmt.Sprintf("You were mentioned to reply to the message msg%d by this message:", replyMessageId) + text
		}
	}

	var mediaSource *tgbotapi.Message
	if len(message.Photo) > 0 || message.Sticker != nil {
		mediaSource = message
	} else if message.ReplyToMessage != nil &&
		(len(message.ReplyToMessage.Photo) > 0 || message.ReplyToMessage.Sticker != nil) {
		mediaSource = message.ReplyToMessage
	}

	// Prepare the user content for the model (include image if present)
	var userContent interface{}
	if mediaSource != nil {
		dataURL, err := downloadImageAsDataURL(mediaSource)
		if err != nil {
			log.Printf("Error retrieving image: %v", err)
			errMsg := tgbotapi.NewMessage(message.Chat.ID, fmt.Sprintf("Error processing image: %v", err))
			sendMessage(errMsg, false)
			return
		}

		contentList := []map[string]interface{}{}
		if text != "" {
			contentList = append(contentList, map[string]interface{}{
				"type": "text",
				"text": text,
			})
		}
		contentList = append(contentList, map[string]interface{}{
			"type": "image_url",
			"image_url": map[string]string{
				"url": dataURL,
			},
		})
		userContent = contentList
	} else {
		userContent = text
	}

	// Determine which model to use (web search or normal) based on triggers
	replyContext := ""
	if message.ReplyToMessage != nil {
		replyContext = message.ReplyToMessage.Text
	}

	useSearchModel := shouldUseWebSearch(text, replyContext)
	fmt.Println("useSearchModel: %v\n", useSearchModel)
	modelName := gptModelForChatting
	if useSearchModel && mediaSource == nil {
		modelName = gptModelForWebSearch
	}
	// Set reasoning/verbosity (as before)
	var reasoning *string
	var verbosity *string
	if !useSearchModel {
		v, r := "low", "low"
		verbosity, reasoning = &v, &r
	}

	limit := 300
	if useSearchModel {
		limit = 10
	}

	messagesString, err := getFormattedMessages(message.Chat.ID, limit)
	if err != nil {
		log.Printf("Error getting formatted messages: %v", err)
		return
	}

	systemPrompt, err := db.GetSystemPrompt(true)
	currentDate := strings.ToUpper(time.Now().Format("02-Jan-2006 15:04:05"))
	systemPrompt = strings.Replace(systemPrompt, "%current_date%", currentDate, 1)
	if err != nil {
		log.Fatal(err)
	}
	if messagesString != "" {
		systemPrompt += "\n\n**Chat history:**\n" + messagesString
	}

	// Create the chat completion request with system prompt and user content
	requestBody := api.ChatCompletionRequest{
		Model:           modelName,
		ReasoningEffort: reasoning,
		Verbosity:       verbosity,
		Messages: []api.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userContent},
		},
	}

	completionResponse, err := api.GetChatCompletion(openAIToken, requestBody)

	var gptResponseText string
	if err != nil {
		gptResponseText = fmt.Sprintf("Error getting chat completion: %v\n", err)
	} else if len(completionResponse.Choices) > 0 {
		gptResponseText = messageContentToString(completionResponse.Choices[0].Message.Content)
	} else {
		gptResponseText = "No choices in response"
	}

	msg := tgbotapi.NewMessage(message.Chat.ID, gptResponseText)
	msg.ReplyToMessageID = message.MessageID
	msg.ParseMode = tgbotapi.ModeMarkdownV2
	sendMessage(msg, err == nil)
}

func messageContentToString(content interface{}) string {
	// Most common case – plain string
	if s, ok := content.(string); ok {
		return s
	}

	// If model ever sends multi-part content (array of segments)
	if parts, ok := content.([]interface{}); ok {
		var sb strings.Builder
		for _, p := range parts {
			m, ok := p.(map[string]interface{})
			if !ok {
				continue
			}
			t, _ := m["type"].(string)
			switch t {
			case "text":
				if txt, ok := m["text"].(string); ok {
					sb.WriteString(txt)
				}
			}
		}
		if sb.Len() > 0 {
			return sb.String()
		}
		// Fallback: dump as %v so хоть что-то вернём
		return fmt.Sprintf("%v", content)
	}

	// Fallback for any other unexpected shape
	return fmt.Sprintf("%v", content)
}

func containsTrigger(s string) bool {
	if s == "" {
		return false
	}

	lower := strings.ToLower(s)

	triggers := []string{
		"загугли",
		"погугли",
		"гугли",
		"найди",
		"поищи",
		"google",
		"search",
	}
	for _, t := range triggers {
		if t == "" {
			continue
		}

		tLower := strings.ToLower(t)
		if strings.Contains(lower, tLower) {
			return true
		}
	}

	urlPattern := `(?i)\b(?:https?://|www\.)\S+`
	matched, _ := regexp.MatchString(urlPattern, s)
	return matched
}

func shouldUseWebSearch(userText, replyText string) bool {
	combined := strings.TrimSpace(userText)
	if replyText != "" {
		combined = strings.TrimSpace(combined + "\n" + replyText)
	}

	if containsTrigger(combined) {
		return true
	}
	fmt.Println("gptModelForRouting: %s\n", gptModelForRouting)

	if combined == "" || gptModelForRouting == "" {
		return false
	}

	requestBody := api.ChatCompletionRequest{
		Model: gptModelForRouting,
		Messages: []api.Message{
			{
				Role: "system",
				Content: "You are a router. Decide if the user text requires live web search. " +
					"Return exactly one token: SEARCH (if web search is needed) or NO_SEARCH (if not). " +
					"Use SEARCH for queries asking to search, containing URLs, or requesting fresh info; otherwise NO_SEARCH.",
			},
			{
				Role:    "user",
				Content: fmt.Sprintf("Message to classify:\n%s", combined),
			},
		},
	}

	resp, err := api.GetChatCompletion(openAIToken, requestBody)
	fmt.Println("resp: %v\n", resp)
	if err != nil {
		log.Println("Routing model error: %v", err)
		return false
	}

	if len(resp.Choices) == 0 {
		return false
	}

	decision := strings.ToUpper(strings.TrimSpace(messageContentToString(resp.Choices[0].Message.Content)))

	// Use only the first token to avoid substring collisions (e.g., NO_SEARCH contains SEARCH).
	if fields := strings.Fields(decision); len(fields) > 0 {
		decision = fields[0]
	}
	decision = strings.Trim(decision, " .!,")

	if strings.HasPrefix(decision, "NO_SEARCH") {
		return false
	}
	if strings.HasPrefix(decision, "SEARCH") {
		return true
	}
	return false
}

func aggregateBotMessage(text string) (*string, error) {
	requestBody := api.ChatCompletionRequest{
		Model: gptModelForChatting,
		Messages: []api.Message{
			{
				Role: "system",
				Content: "You are a summarizer. Create a concise summary of the assistant's reply in 150-300 characters. " +
					"Keep key facts, names, and numbers. Return plain text without markdown, lists, or introductions.",
			},
			{
				Role:    "user",
				Content: fmt.Sprintf("Summarize this reply:\n%s", text),
			},
		},
	}

	resp, err := api.GetChatCompletion(openAIToken, requestBody)
	if err != nil {
		return nil, err
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in aggregation response")
	}

	summary := strings.TrimSpace(messageContentToString(resp.Choices[0].Message.Content))
	if summary == "" {
		return nil, fmt.Errorf("empty aggregation response")
	}

	const maxSummaryLength = 300
	if utf8.RuneCountInString(summary) > maxSummaryLength {
		summaryRunes := []rune(summary)
		summary = string(summaryRunes[:maxSummaryLength])
	}

	return &summary, nil
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

		messageText := msg.Text
		if msg.AggregatedText != nil && *msg.AggregatedText != "" {
			messageText = *msg.AggregatedText
		}

		messageLine := fmt.Sprintf("msg%d %s %s : %s\n", msg.MessageID, formattedDate, username, messageText)
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

	var aggregatedText *string
	isBotMessage := message.From != nil && message.From.ID == bot.Self.ID
	if isBotMessage && utf8.RuneCountInString(text) > 300 {
		summary, err := aggregateBotMessage(text)
		if err != nil {
			log.Printf("Error aggregating bot message %d: %v", message.MessageID, err)
		} else {
			aggregatedText = summary
		}
	}

	err := db.SaveMessage(
		message.MessageID,
		message.Chat.ID,
		message.From.ID,
		text,
		aggregatedText,
		message.Date,
	)
	if err != nil {
		log.Printf("Error saving message: %v", err)
	}
}
