package main

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"pet.outbid.goapp/api"
	"pet.outbid.goapp/db"
)

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

func handleMessage(message *tgbotapi.Message) {
	recordMediaGroup(message)

	var text string
	if message.Text != "" {
		text = message.Text
	} else if message.Caption != "" {
		text = message.Caption
	} else if hasSupportedMedia(message) {
		// If the message contains media with no text/caption
		replyToBotMessage := message.ReplyToMessage != nil && bot.Self.ID == message.ReplyToMessage.From.ID
		if replyToBotMessage {
			// If it's a reply to the bot's message, handle it as a bot mention (including the media)
			handleMention(message)
		} else {
			log.Printf("Received media without text (message_id: %d), ignoring.", message.MessageID)
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
	messages := []api.Message{
		{
			Role:    "system",
			Content: "You are a helpful assistant.",
		},
		{
			Role:    "user",
			Content: args,
		},
	}

	completionResponse, err := api.CallChatCompletion(
		openAIToken,
		gptModelForChatting,
		messages,
		api.ChatOptions{Reasoning: &reasoning, Verbosity: &verbosity},
	)
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

	mediaMessages := collectMediaMessages(message)

	// Prepare the user content for the model (include image if present)
	var userContent interface{}
	if len(mediaMessages) > 0 {
		dataURLs, err := downloadMediaMessagesAsDataURLs(mediaMessages)
		if err != nil {
			log.Printf("Error retrieving media: %v", err)
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
		for _, dataURL := range dataURLs {
			contentList = append(contentList, map[string]interface{}{
				"type": "image_url",
				"image_url": map[string]string{
					"url": dataURL,
				},
			})
		}
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
	if useSearchModel && len(mediaMessages) == 0 {
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
	messages := []api.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userContent},
	}

	completionResponse, err := api.CallChatCompletion(
		openAIToken,
		modelName,
		messages,
		api.ChatOptions{Reasoning: reasoning, Verbosity: verbosity},
	)

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
