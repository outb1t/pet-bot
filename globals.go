package main

import (
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
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

type mediaGroupEntry struct {
	messages map[int]*tgbotapi.Message
	updated  time.Time
}

const mediaGroupCacheTTL = 1 * time.Hour

var (
	mediaGroupCache     = make(map[string]*mediaGroupEntry)
	mediaGroupCacheLock sync.Mutex
)
