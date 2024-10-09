package db

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

var DB *sql.DB

type Message struct {
	MessageID int
	ChatID    int64
	UserID    int64
	Text      string
	Date      time.Time
}

var (
	promptCache      string
	promptCacheTime  time.Time
	promptCacheMutex sync.RWMutex
	cacheDuration    = 15 * time.Second
)

func InitDB() error {
	var err error
	dbHost := os.Getenv("DB_HOST")
	dbPort := os.Getenv("DB_PORT")
	dbUser := os.Getenv("DB_USER")
	dbPassword := os.Getenv("DB_PASSWORD")
	dbName := os.Getenv("DB_NAME")

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true",
		dbUser, dbPassword, dbHost, dbPort, dbName)

	DB, err = sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("error opening database: %v", err)
	}

	err = DB.Ping()
	if err != nil {
		return fmt.Errorf("error connecting to database: %v", err)
	}

	log.Println("Connected to the database successfully")
	return nil
}

func SaveMessage(messageID int, chatID int64, userID int64, text string, date int) error {
	query := `
        INSERT INTO messages (message_id, chat_id, user_id, text, date)
        VALUES (?, ?, ?, ?, FROM_UNIXTIME(?))
    `
	_, err := DB.Exec(query, messageID, chatID, userID, text, date)
	if err != nil {
		log.Printf("Error saving message to database: %v", err)
		return err
	}
	return nil
}

func GetLastMessages(chatID int64, limit int) ([]Message, error) {
	query := `
        SELECT *
        FROM (
            SELECT message_id, chat_id, user_id, text, date
            FROM messages
            WHERE chat_id = ?
            ORDER BY date DESC
            LIMIT ?
        ) sub
        ORDER BY message_id
    `

	rows, err := DB.Query(query, chatID, limit)
	if err != nil {
		return nil, fmt.Errorf("Error querying messages: %v", err)
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		if err := rows.Scan(&msg.MessageID, &msg.ChatID, &msg.UserID, &msg.Text, &msg.Date); err != nil {
			return nil, fmt.Errorf("Error scanning row: %v", err)
		}
		messages = append(messages, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("Error with rows: %v", err)
	}

	return messages, nil
}

func GetSystemPrompt() (string, error) {
	promptCacheMutex.RLock()
	if time.Since(promptCacheTime) < cacheDuration && promptCache != "" {
		cachedPrompt := promptCache
		promptCacheMutex.RUnlock()
		return cachedPrompt, nil
	}
	promptCacheMutex.RUnlock()

	promptCacheMutex.Lock()
	defer promptCacheMutex.Unlock()

	if time.Since(promptCacheTime) < cacheDuration && promptCache != "" {
		return promptCache, nil
	}

	query := `
        SELECT prompt
        FROM prompts
        WHERE type = ?
        ORDER BY id DESC
        LIMIT 1
    `

	var promptText string
	err := DB.QueryRow(query, 1).Scan(&promptText)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("no prompt found with type = 1")
		}
		return "", fmt.Errorf("error scanning row: %v", err)
	}

	promptCache = promptText
	promptCacheTime = time.Now()

	return promptText, nil
}
