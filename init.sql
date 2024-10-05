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