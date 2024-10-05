package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"

	_ "github.com/go-sql-driver/mysql"
)

var DB *sql.DB

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
