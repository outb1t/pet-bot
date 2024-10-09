CREATE TABLE IF NOT EXISTS messages
(
    id         INT AUTO_INCREMENT PRIMARY KEY,
    message_id INT      NOT NULL,
    chat_id    BIGINT   NOT NULL,
    user_id    BIGINT   NOT NULL,
    text       TEXT,
    date       DATETIME NOT NULL
);

CREATE INDEX idx_date ON messages(date);
CREATE INDEX idx_message_id ON messages(message_id);

CREATE TABLE IF NOT EXISTS prompts
(
    id INT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    type TINYINT UNSIGNED NOT NULL,
    prompt TEXT,
    date DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
);

CREATE INDEX idx_type_id ON prompts (type, id);
