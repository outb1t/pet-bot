package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image/gif"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	maxImageSize = 2 * 1024 * 1024
	maxVideoSize = 10 * 1024 * 1024
)

type mediaItem struct {
	FileID         string
	MimeType       string
	FileName       string
	Kind           string
	FallbackFileID string
}

func hasSupportedMedia(message *tgbotapi.Message) bool {
	if message == nil {
		return false
	}

	if len(message.Photo) > 0 {
		return true
	}

	if message.Sticker != nil {
		return !message.Sticker.IsAnimated
	}

	if message.Animation != nil {
		return true
	}

	if message.Document != nil {
		mimeType := strings.ToLower(message.Document.MimeType)
		if strings.HasPrefix(mimeType, "image/") {
			return true
		}
		name := strings.ToLower(message.Document.FileName)
		return strings.HasSuffix(name, ".gif")
	}

	return false
}

func mediaGroupKey(chatID int64, groupID string) string {
	return fmt.Sprintf("%d:%s", chatID, groupID)
}

func cleanupMediaGroupCacheLocked(now time.Time) {
	cutoff := now.Add(-mediaGroupCacheTTL)
	for key, entry := range mediaGroupCache {
		if entry.updated.Before(cutoff) {
			delete(mediaGroupCache, key)
		}
	}
}

func recordMediaGroup(message *tgbotapi.Message) {
	if message == nil || message.MediaGroupID == "" || !hasSupportedMedia(message) {
		return
	}

	key := mediaGroupKey(message.Chat.ID, message.MediaGroupID)

	mediaGroupCacheLock.Lock()
	defer mediaGroupCacheLock.Unlock()

	entry := mediaGroupCache[key]
	if entry == nil {
		entry = &mediaGroupEntry{
			messages: make(map[int]*tgbotapi.Message),
		}
		mediaGroupCache[key] = entry
	}

	msgCopy := *message
	entry.messages[message.MessageID] = &msgCopy
	entry.updated = time.Now()
	cleanupMediaGroupCacheLocked(entry.updated)
}

func getMediaGroupMessages(chatID int64, groupID string) []*tgbotapi.Message {
	key := mediaGroupKey(chatID, groupID)

	mediaGroupCacheLock.Lock()
	entry := mediaGroupCache[key]
	if entry == nil {
		mediaGroupCacheLock.Unlock()
		return nil
	}
	entry.updated = time.Now()
	messages := make([]*tgbotapi.Message, 0, len(entry.messages))
	for _, msg := range entry.messages {
		messages = append(messages, msg)
	}
	mediaGroupCacheLock.Unlock()

	sort.Slice(messages, func(i, j int) bool {
		return messages[i].MessageID < messages[j].MessageID
	})
	return messages
}

func collectMediaMessages(message *tgbotapi.Message) []*tgbotapi.Message {
	if message == nil {
		return nil
	}

	if hasSupportedMedia(message) {
		if message.MediaGroupID != "" {
			groupMessages := getMediaGroupMessages(message.Chat.ID, message.MediaGroupID)
			if len(groupMessages) > 0 {
				return groupMessages
			}
		}
		return []*tgbotapi.Message{message}
	}

	if message.ReplyToMessage != nil && hasSupportedMedia(message.ReplyToMessage) {
		if message.ReplyToMessage.MediaGroupID != "" {
			groupMessages := getMediaGroupMessages(message.Chat.ID, message.ReplyToMessage.MediaGroupID)
			if len(groupMessages) > 0 {
				return groupMessages
			}
		}
		return []*tgbotapi.Message{message.ReplyToMessage}
	}

	return nil
}

func extractMediaItems(message *tgbotapi.Message) []mediaItem {
	if message == nil {
		return nil
	}

	if len(message.Photo) > 0 {
		photo := message.Photo[len(message.Photo)-1]
		return []mediaItem{{FileID: photo.FileID, Kind: "photo"}}
	}

	if message.Sticker != nil {
		if message.Sticker.IsAnimated {
			return nil
		}
		return []mediaItem{{FileID: message.Sticker.FileID, Kind: "sticker"}}
	}

	if message.Animation != nil {
		item := mediaItem{
			FileID:   message.Animation.FileID,
			MimeType: message.Animation.MimeType,
			FileName: message.Animation.FileName,
			Kind:     "animation",
		}
		if message.Animation.Thumbnail != nil {
			item.FallbackFileID = message.Animation.Thumbnail.FileID
		}
		return []mediaItem{item}
	}

	if message.Document != nil {
		mimeType := strings.ToLower(message.Document.MimeType)
		fileName := strings.ToLower(message.Document.FileName)
		if strings.HasPrefix(mimeType, "image/") || strings.HasSuffix(fileName, ".gif") {
			return []mediaItem{{
				FileID:   message.Document.FileID,
				MimeType: message.Document.MimeType,
				FileName: message.Document.FileName,
				Kind:     "document",
			}}
		}
	}

	return nil
}

func downloadMediaMessagesAsDataURLs(messages []*tgbotapi.Message) ([]string, error) {
	var urls []string
	for _, msg := range messages {
		items := extractMediaItems(msg)
		for _, item := range items {
			itemURLs, err := downloadMediaItemAsDataURLs(item)
			if err != nil {
				return nil, err
			}
			urls = append(urls, itemURLs...)
		}
	}

	if len(urls) == 0 {
		return nil, fmt.Errorf("no supported media found")
	}

	return urls, nil
}

func downloadMediaItemAsDataURLs(item mediaItem) ([]string, error) {
	maxSize := maxImageSize
	if item.Kind == "animation" || isVideoByMeta(item.MimeType, item.FileName) {
		maxSize = maxVideoSize
	}

	data, contentType, err := downloadFileBytes(item.FileID, maxSize)
	if err != nil {
		return nil, err
	}
	if contentType == "" && item.MimeType != "" {
		contentType = item.MimeType
	}

	if item.Kind == "animation" || isVideoByMeta(item.MimeType, item.FileName) {
		urls, err := videoFramesToDataURLs(data)
		if err != nil && item.FallbackFileID != "" {
			fallbackData, fallbackType, fallbackErr := downloadFileBytes(item.FallbackFileID, maxImageSize)
			if fallbackErr == nil {
				return fileDataToDataURLs(fallbackData, fallbackType)
			}
		}
		return urls, err
	}

	return fileDataToDataURLs(data, contentType)
}

func downloadFileBytes(fileID string, maxSize int) ([]byte, string, error) {
	file, err := bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return nil, "", fmt.Errorf("failed to get file info from Telegram: %w", err)
	}

	fileURL := file.Link(bot.Token)

	resp, err := http.Get(fileURL)
	if err != nil {
		return nil, "", fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read image body: %w", err)
	}

	if maxSize > 0 && len(data) > maxSize {
		return nil, "", fmt.Errorf("image too large (%d bytes), limit is %d bytes", len(data), maxSize)
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}

	return data, contentType, nil
}

func fileDataToDataURLs(data []byte, contentType string) ([]string, error) {
	detectedType := http.DetectContentType(data)
	if contentType == "" {
		contentType = detectedType
	}

	if isGIF(data, contentType) || isGIF(data, detectedType) {
		return gifFramesToDataURLs(data)
	}

	contentTypeLower := strings.ToLower(contentType)
	detectedLower := strings.ToLower(detectedType)
	if strings.HasPrefix(contentTypeLower, "image/") || strings.HasPrefix(detectedLower, "image/") {
		if !strings.HasPrefix(contentTypeLower, "image/") {
			contentType = detectedType
		}
		return []string{dataToDataURL(data, contentType)}, nil
	}

	return nil, fmt.Errorf("unsupported media type: %s", contentType)
}

func dataToDataURL(data []byte, contentType string) string {
	if contentType == "" {
		contentType = "image/jpeg"
	}
	b64 := base64.StdEncoding.EncodeToString(data)
	return fmt.Sprintf("data:%s;base64,%s", contentType, b64)
}

func isGifByMeta(mimeType, fileName string) bool {
	mimeType = strings.ToLower(mimeType)
	if strings.Contains(mimeType, "gif") {
		return true
	}
	fileName = strings.ToLower(fileName)
	return strings.HasSuffix(fileName, ".gif")
}

func isVideoByMeta(mimeType, fileName string) bool {
	mimeType = strings.ToLower(mimeType)
	if strings.HasPrefix(mimeType, "video/") {
		return true
	}
	fileName = strings.ToLower(fileName)
	return strings.HasSuffix(fileName, ".mp4") ||
		strings.HasSuffix(fileName, ".webm") ||
		strings.HasSuffix(fileName, ".mov") ||
		strings.HasSuffix(fileName, ".mkv") ||
		strings.HasSuffix(fileName, ".avi")
}

func isGIF(data []byte, contentType string) bool {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "gif") {
		return true
	}
	if len(data) < 6 {
		return false
	}
	return bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a"))
}

func gifFrameIndices(frameCount int) []int {
	if frameCount <= 0 {
		return nil
	}

	second := 0
	if frameCount > 1 {
		second = 1
	}
	middle := frameCount / 2
	preLast := 0
	if frameCount > 1 {
		preLast = frameCount - 2
	}

	// If the GIF is very short, some indices can repeat; that's fine.
	return []int{second, middle, preLast}
}

func gifFramesToDataURLs(data []byte) ([]string, error) {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to decode gif: %w", err)
	}
	if len(g.Image) == 0 {
		return nil, fmt.Errorf("gif has no frames")
	}

	indices := gifFrameIndices(len(g.Image))
	urls := make([]string, 0, len(indices))

	for _, idx := range indices {
		if idx < 0 || idx >= len(g.Image) {
			continue
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, g.Image[idx]); err != nil {
			return nil, fmt.Errorf("failed to encode gif frame: %w", err)
		}
		urls = append(urls, dataToDataURL(buf.Bytes(), "image/png"))
	}

	if len(urls) == 0 {
		return nil, fmt.Errorf("gif has no usable frames")
	}

	return urls, nil
}

func videoFrameTimestamps(duration float64) []float64 {
	if duration <= 0 {
		return nil
	}

	offsets := []float64{0.2, 0.5, 0.8}
	timestamps := make([]float64, 0, len(offsets))
	for _, offset := range offsets {
		timestamp := duration * offset
		timestamps = append(timestamps, clampVideoTimestamp(timestamp, duration))
	}
	return timestamps
}

func clampVideoTimestamp(timestamp, duration float64) float64 {
	if duration <= 0 {
		return 0
	}
	min := 0.05
	max := duration - 0.05
	if max < min {
		min = 0
		max = duration
	}
	if timestamp < min {
		return min
	}
	if timestamp > max {
		return max
	}
	return timestamp
}

func videoFramesToDataURLs(data []byte) ([]string, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, fmt.Errorf("ffmpeg not available")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		return nil, fmt.Errorf("ffprobe not available")
	}

	tmp, err := os.CreateTemp("", "tg-video-*.mp4")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return nil, fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("failed to close temp file: %w", err)
	}

	duration, err := probeVideoDuration(tmp.Name())
	if err != nil {
		return nil, err
	}

	timestamps := videoFrameTimestamps(duration)
	if len(timestamps) == 0 {
		return nil, fmt.Errorf("video duration is invalid")
	}

	urls := make([]string, 0, len(timestamps))
	for _, timestamp := range timestamps {
		frame, err := extractVideoFrame(tmp.Name(), timestamp)
		if err != nil {
			continue
		}
		urls = append(urls, dataToDataURL(frame, "image/png"))
	}

	if len(urls) == 0 {
		return nil, fmt.Errorf("no video frames extracted")
	}

	return urls, nil
}

func probeVideoDuration(path string) (float64, error) {
	cmd := exec.Command(
		"ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	)
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe failed: %w", err)
	}

	value := strings.TrimSpace(string(output))
	if value == "" {
		return 0, fmt.Errorf("ffprobe returned empty duration")
	}

	duration, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid duration: %w", err)
	}

	if duration <= 0 {
		return 0, fmt.Errorf("invalid duration: %.4f", duration)
	}

	return duration, nil
}

func extractVideoFrame(path string, timestamp float64) ([]byte, error) {
	cmd := exec.Command(
		"ffmpeg",
		"-v", "error",
		"-ss", fmt.Sprintf("%.3f", timestamp),
		"-i", path,
		"-frames:v", "1",
		"-f", "image2pipe",
		"-vcodec", "png",
		"pipe:1",
	)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("ffmpeg failed: %v (%s)", err, strings.TrimSpace(stderr.String()))
	}

	if stdout.Len() == 0 {
		return nil, fmt.Errorf("ffmpeg returned empty frame")
	}

	return stdout.Bytes(), nil
}
